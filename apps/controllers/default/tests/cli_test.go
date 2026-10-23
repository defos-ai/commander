package default_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/controllers/default/cli"
	"github.com/defos-ai/commander/framework/domain/controllers"
	fscontrollers "github.com/defos-ai/commander/providers/fs/controllers"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func TestCLIRegistersController(t *testing.T) {
	t.Setenv("ENV", "")
	root := t.TempDir()
	store, err := fscontrollers.NewControllerFsStore(filepath.Join(root, "database"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	var ready controllers.Controller
	err = cli.RunWithOptions(ctx, []string{
		"--bus-url=" + filepath.Join(root, "bus"),
		"--storage-url=" + filepath.Join(root, "storage"),
		"--database-url=" + filepath.Join(root, "database"),
	}, io.Discard, cli.Options{Ready: func() {
		defer cancel()
		records, readErr := store.List(ctx)
		if readErr != nil || len(records) != 1 {
			t.Errorf("controller registration unavailable at readiness: records=%v error=%v", records, readErr)
			return
		}
		ready = records[0]
	}})
	if err != nil {
		t.Fatal(err)
	}
	if ready.Status != controllers.Ready || ready.Type != "default" || ready.AgentSelector != "round-robin" || ready.RuleSelector != "mission-rules" || ready.Version == "" {
		t.Fatalf("controller did not advertise readiness: %+v", ready)
	}
	record, err := store.Get(t.Context(), ready.ID)
	if err != nil || record.Status != controllers.Stopped || !record.LastHeartbeat.After(ready.LastHeartbeat) {
		t.Fatalf("controller shutdown was not recorded: record=%+v error=%v", record, err)
	}
}

func TestCLIRejectsUnavailableProviders(t *testing.T) {
	const secret = "provider-test-secret"
	for _, test := range []struct {
		name, flag, url       string
		dependency            string
		invalidConfig         bool
		filesystemForm        string
		connectionFailure     bool
		initializationFailure bool
	}{
		{name: "Mongo connection unavailable", flag: "database-url", url: "mongodb://user:" + secret + "@127.0.0.1:1/commander?serverSelectionTimeoutMS=50", connectionFailure: true},
		{name: "malformed Mongo query", flag: "database-url", url: "mongodb://user:" + secret + "@127.0.0.1:1/commander?tlsCertificateKeyFilePassword=" + secret + "%zz", initializationFailure: true},
		{name: "unsupported database", flag: "database-url", url: "postgres://user:" + secret + "@127.0.0.1:5432/commander"},
		{name: "RabbitMQ bus unavailable", flag: "bus-url", url: "amqp://user:" + secret + "@127.0.0.1:1", dependency: "rabbitmq"},
		{name: "RabbitMQ TLS bus unavailable", flag: "bus-url", url: "amqps://user:" + secret + "@127.0.0.1:1", dependency: "rabbitmq"},
		{name: "unsupported broker", flag: "bus-url", url: "nats://user:" + secret + "@127.0.0.1:4222"},
		{name: "S3 URL with credentials", flag: "storage-url", url: "s3://user:" + secret + "@artifacts/bucket", invalidConfig: true},
		{name: "uppercase FILE URL", flag: "database-url", url: "postgres://127.0.0.1:5432/commander", filesystemForm: "FILE"},
		{name: "uppercase FS URL", flag: "database-url", url: "postgres://127.0.0.1:5432/commander", filesystemForm: "FS"},
		{name: "absolute path containing colon", flag: "database-url", url: "postgres://127.0.0.1:5432/commander", filesystemForm: "path"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.filesystemForm == "path" && runtime.GOOS == "windows" {
				t.Skip("Windows does not support colons in filenames")
			}
			root := t.TempDir()
			fileURL := func(name string) string {
				if test.filesystemForm == "path" {
					return filepath.Join(root, name+":1")
				}
				scheme := test.filesystemForm
				if scheme == "" {
					scheme = "file"
				}
				return (&url.URL{Scheme: scheme, Path: filepath.Join(root, name)}).String()
			}
			args := []string{
				"--bus-url=" + fileURL("bus"),
				"--storage-url=" + fileURL("storage"),
				"--database-url=" + fileURL("database"),
				"--heartbeat-timeout=45s",
				"--log-level=info", "--log-format=json",
				"--" + test.flag + "=" + test.url,
			}
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			var logs bytes.Buffer

			err := cli.Run(ctx, args, &logs)

			if ctx.Err() != nil {
				t.Fatalf("startup did not return within its deadline: %v", err)
			}
			if err == nil {
				t.Fatal("startup accepted an unavailable provider")
			}
			message := strings.ToLower(err.Error())
			if !strings.Contains(message, test.flag) {
				t.Errorf("error does not identify %s: %v", test.flag, err)
			}
			if test.invalidConfig {
				if !strings.Contains(message, "credentials") && !strings.Contains(message, "userinfo") {
					t.Errorf("startup did not explain invalid S3 configuration: %v", err)
				}
			} else if test.connectionFailure {
				if !mongo.IsTimeout(err) {
					t.Errorf("expected a MongoDB connection timeout: %v", err)
				}
			} else if test.initializationFailure {
				if !strings.Contains(message, "mongodb initialization failed") {
					t.Error("startup did not identify the MongoDB initialization failure")
				}
			} else if test.dependency != "" {
				var connectionError *net.OpError
				if !errors.As(err, &connectionError) || !strings.Contains(message, test.dependency) {
					t.Errorf("startup must report a %s connection failure: %v", test.dependency, err)
				}
			} else if !strings.Contains(message, "unsupported") && !strings.Contains(message, "not implemented") {
				t.Errorf("startup did not explain the unavailable provider: %v", err)
			}
			if strings.Contains(err.Error(), secret) || strings.Contains(logs.String(), secret) {
				t.Error("startup exposed credentials from the connection URL")
			}
		})
	}
}

func TestCLIStartsWithMongo(t *testing.T) {
	databaseURL := os.Getenv("COMMANDER_TEST_MONGO_URL")
	if databaseURL == "" {
		t.Skip("COMMANDER_TEST_MONGO_URL is not set")
	}
	base, query, hasQuery := strings.Cut(databaseURL, "?")
	databaseURL = base + "_controller_startup"
	if hasQuery {
		databaseURL += "?" + query
	}
	t.Setenv("ENV", "")
	root := t.TempDir()
	args := []string{
		"--bus-url=" + filepath.Join(root, "bus"),
		"--storage-url=" + filepath.Join(root, "storage"),
		"--database-url=" + databaseURL,
		"--heartbeat-timeout=45s",
		"--log-level=info", "--log-format=json",
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	ready := false

	err := cli.RunWithOptions(ctx, args, io.Discard, cli.Options{Ready: func() {
		ready = true
		cancel()
	}})

	if err != nil || !ready {
		t.Fatalf("MongoDB host did not start and stop cleanly: ready=%t error=%v", ready, err)
	}
}
