package web_test

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/agents/web/cli"
)

func TestCLIRejectsUnavailableProviders(t *testing.T) {
	t.Setenv("SECRET_ENCRYPTION_KEY", "")
	const secret = "provider-test-secret"
	for _, test := range []struct {
		name, flag, url string
		dependency      string
		invalidConfig   bool
		filesystemForm  string
	}{
		{name: "RabbitMQ bus unavailable", flag: "bus-url", url: "amqp://user:" + secret + "@127.0.0.1:1", dependency: "rabbitmq"},
		{name: "RabbitMQ TLS bus unavailable", flag: "bus-url", url: "amqps://user:" + secret + "@127.0.0.1:1", dependency: "rabbitmq"},
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
				t.Fatalf("startup did not report the unavailable dependency before its deadline: %v", err)
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
				if !strings.Contains(message, test.dependency) || !strings.Contains(message, "connect failed") {
					t.Errorf("startup must identify the failed %s connection: %v", test.dependency, err)
				}
				var connectionErr *net.OpError
				if !errors.As(err, &connectionErr) {
					t.Errorf("startup must preserve the underlying connection error: %v", err)
				}
			} else if !strings.Contains(message, "unsupported") {
				t.Errorf("startup did not explain the unavailable provider: %v", err)
			}
			if strings.Contains(err.Error(), secret) || strings.Contains(logs.String(), secret) {
				t.Error("startup exposed credentials from the connection URL")
			}
		})
	}
}

func TestCLISecretKeyValidationPrecedesProviderStartup(t *testing.T) {
	const invalidKey = "private-invalid-encryption-key"
	t.Setenv("SECRET_ENCRYPTION_KEY", invalidKey)
	root := filepath.Join(t.TempDir(), "unused")
	var logs bytes.Buffer
	err := cli.Run(t.Context(), []string{
		"--bus-url=" + filepath.Join(root, "bus"),
		"--storage-url=" + filepath.Join(root, "storage"),
	}, &logs)
	if err == nil || !strings.Contains(err.Error(), "SECRET_ENCRYPTION_KEY") {
		t.Fatal("invalid encryption key did not fail configuration")
	}
	if strings.Contains(err.Error(), invalidKey) || strings.Contains(logs.String(), invalidKey) {
		t.Fatal("invalid encryption key appeared in diagnostics")
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("agent opened a provider before validating the encryption key")
	}
	logs.Reset()
	if err := cli.Run(t.Context(), []string{"--help"}, &logs); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "SECRET_ENCRYPTION_KEY") || strings.Contains(logs.String(), invalidKey) {
		t.Fatal("help must describe the encryption setting without exposing its value")
	}
}
