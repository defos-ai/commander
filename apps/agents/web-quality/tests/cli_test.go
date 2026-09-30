package webquality_test

import (
	"bytes"
	"context"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"commander/apps/agents/web-quality/cli"
)

func TestCLIRejectsUnavailableProviders(t *testing.T) {
	const secret = "provider-test-secret"
	for _, test := range []struct {
		name, flag, url string
		dependency      string
		invalidConfig   bool
		filesystemForm  string
	}{
		{name: "RabbitMQ bus unavailable", flag: "bus-url", url: "amqp://user:" + secret + "@127.0.0.1:5672", dependency: "rabbitmq"},
		{name: "RabbitMQ TLS bus unavailable", flag: "bus-url", url: "amqps://user:" + secret + "@127.0.0.1:5671", dependency: "rabbitmq"},
		{name: "unsupported broker", flag: "bus-url", url: "nats://user:" + secret + "@127.0.0.1:4222"},
		{name: "S3 URL with credentials", flag: "storage-url", url: "s3://user:" + secret + "@artifacts/bucket", invalidConfig: true},
		{name: "uppercase FILE URL", flag: "bus-url", url: "nats://127.0.0.1:4222", filesystemForm: "FILE"},
		{name: "uppercase FS URL", flag: "bus-url", url: "nats://127.0.0.1:4222", filesystemForm: "FS"},
		{name: "absolute path containing colon", flag: "bus-url", url: "nats://127.0.0.1:4222", filesystemForm: "path"},
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
			if test.invalidConfig {
				if !strings.Contains(message, "credentials") && !strings.Contains(message, "userinfo") {
					t.Errorf("startup did not explain invalid S3 configuration: %v", err)
				}
			} else if test.dependency != "" {
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
