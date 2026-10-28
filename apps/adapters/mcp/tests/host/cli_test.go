package mcp_test

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/adapters/mcp/cli"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func TestCLIRejectsUnavailableProviders(t *testing.T) {
	t.Setenv("MCP_TOKEN", "mcp-fixture-token")
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
		{name: "incomplete S3 credentials", flag: "storage-url", url: "s3://:" + secret + "@artifacts/bucket", invalidConfig: true},
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
				"--listen-address=127.0.0.1:0",
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
				if !strings.Contains(message, test.dependency) || !errors.As(err, &connectionError) {
					t.Errorf("expected a %s connection failure: %v", test.dependency, err)
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
