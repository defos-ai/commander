package mcp_test

import (
	"bytes"
	"context"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"commander/adapters/mcp/cli"
)

func TestCLIRejectsUnavailableProviders(t *testing.T) {
	const secret = "provider-test-secret"
	for _, test := range []struct {
		name, flag, url string
		dependency      string
		filesystemForm  string
	}{
		{name: "Mongo operation store unavailable", flag: "database-url", url: "mongodb://user:" + secret + "@127.0.0.1:1/commander", dependency: "operation"},
		{name: "Mongo SRV operation store unavailable", flag: "database-url", url: "mongodb+srv://user:" + secret + "@provider.invalid/commander", dependency: "operation"},
		{name: "unsupported database", flag: "database-url", url: "postgres://user:" + secret + "@127.0.0.1:5432/commander"},
		{name: "RabbitMQ bus unavailable", flag: "bus-url", url: "amqp://user:" + secret + "@127.0.0.1:5672", dependency: "rabbitmq"},
		{name: "RabbitMQ TLS bus unavailable", flag: "bus-url", url: "amqps://user:" + secret + "@127.0.0.1:5671", dependency: "rabbitmq"},
		{name: "unsupported broker", flag: "bus-url", url: "nats://user:" + secret + "@127.0.0.1:4222"},
		{name: "unavailable artifact store", flag: "storage-url", url: "s3://user:" + secret + "@artifacts/bucket", dependency: "artifact"},
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
				t.Fatalf("startup waited for provider I/O instead of rejecting the unavailable dependency: %v", err)
			}
			if err == nil {
				t.Fatal("startup accepted an unavailable provider")
			}
			message := strings.ToLower(err.Error())
			if !strings.Contains(message, test.flag) {
				t.Errorf("error does not identify %s: %v", test.flag, err)
			}
			if test.dependency != "" {
				if !strings.Contains(message, test.dependency) || !strings.Contains(message, "not implemented") {
					t.Errorf("startup must identify its unimplemented %s dependency: %v", test.dependency, err)
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
