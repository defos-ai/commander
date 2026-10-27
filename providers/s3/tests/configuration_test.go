package s3_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/defos-ai/commander/providers/s3/store"
)

func isolateAWSConfiguration(t *testing.T) {
	t.Helper()
	directory := t.TempDir()
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(directory, "config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(directory, "credentials"))
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	for _, name := range []string{
		"AWS_PROFILE", "AWS_DEFAULT_PROFILE", "AWS_ROLE_ARN", "AWS_WEB_IDENTITY_TOKEN_FILE",
		"AWS_CONTAINER_CREDENTIALS_FULL_URI", "AWS_CONTAINER_CREDENTIALS_RELATIVE_URI",
	} {
		t.Setenv(name, "")
	}
}

func TestS3Configuration(t *testing.T) {
	isolateAWSConfiguration(t)
	for _, raw := range []string{
		"https://bucket/prefix", "s3://", "s3://bucket:9000/prefix", "s3://Bucket/prefix", "s3://bucket/%zz",
		"s3://user@bucket/prefix", "s3://user:@bucket/prefix", "s3://:secret-marker@bucket/prefix",
		"s3://user:secret-marker%00@bucket/prefix", "s3://user%0A:secret-marker@bucket/prefix",
		"s3://user:secret-marker%7F@bucket/prefix", "s3://user:secret-marker%zz@bucket/prefix",
		"s3://user:secret-marker%C2%85@bucket/prefix",
		"s3://bucket/prefix?token=secret-marker", "s3://bucket/prefix#secret-marker",
		"s3://bucket/../outside", "s3://bucket/prefix/%2e%2e/outside", "s3://bucket/prefix//child", "s3://bucket/prefix/%5cchild",
	} {
		if _, err := store.Open(t.Context(), raw); err == nil || strings.Contains(err.Error(), "secret-marker") {
			t.Fatalf("invalid S3 configuration accepted or leaked a secret: %v", err)
		}
	}
	t.Setenv("S3_USE_PATH_STYLE", "invalid-secret-marker")
	if _, err := store.Open(t.Context(), "s3://bucket/prefix"); err == nil || strings.Contains(err.Error(), "secret-marker") {
		t.Fatalf("invalid path-style setting accepted or leaked its value: %v", err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := store.Open(cancelled, "s3://bucket/prefix"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled constructor: %v", err)
	}
}
