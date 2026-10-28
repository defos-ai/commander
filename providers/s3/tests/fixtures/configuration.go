package fixtures

import (
	"path/filepath"
	"testing"
)

func IsolateAWSConfiguration(t *testing.T) {
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
