package store

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
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

func TestS3CredentialSources(t *testing.T) {
	for _, test := range []struct {
		name, accessKey, secretKey, sessionToken string
		userinfo                                 *url.Userinfo
		rawURL                                   string
		withoutEnvironment                       bool
	}{
		{name: "environment", accessKey: "environment-key", secretKey: "environment-secret", sessionToken: "environment-token"},
		{name: "URL overrides environment", accessKey: "url-key", secretKey: "url-secret", userinfo: url.UserPassword("url-key", "url-secret")},
		{name: "URL decodes reserved characters", accessKey: "url+key@:%", secretKey: "secret/+@:%", userinfo: url.UserPassword("url+key@:%", "secret/+@:%")},
		{name: "raw AWS secret", accessKey: "url-key", secretKey: "raw/secret+key=", rawURL: "s3://url-key:raw/secret+key=@bucket/prefix"},
		{name: "raw secret begins with digits", accessKey: "url-key", secretKey: "123/secret+=", rawURL: "s3://url-key:123/secret+=@bucket/prefix"},
		{name: "raw leading and trailing slashes", accessKey: "url-key", secretKey: "/raw/secret/", rawURL: "s3://url-key:/raw/secret/@bucket/prefix"},
		{name: "raw slash with escaped delimiters", accessKey: "url-key", secretKey: "raw/secret@?#", rawURL: "s3://url-key:raw/secret%40%3F%23@bucket/prefix"},
		{name: "raw slash preserves one decoding pass", accessKey: "url-key", secretKey: "raw/secret%2F", rawURL: "s3://url-key:raw/secret%252F@bucket/prefix"},
		{name: "URL without environment", accessKey: "url-key", secretKey: "url-secret", userinfo: url.UserPassword("url-key", "url-secret"), withoutEnvironment: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			isolateAWSConfiguration(t)
			environment := map[string]string{
				"AWS_ACCESS_KEY_ID": "environment-key", "AWS_SECRET_ACCESS_KEY": "environment-secret", "AWS_SESSION_TOKEN": "environment-token",
			}
			for name, value := range environment {
				if test.withoutEnvironment {
					value = ""
					environment[name] = value
				}
				t.Setenv(name, value)
			}
			t.Setenv("AWS_REGION", "us-east-1")
			t.Setenv("S3_USE_PATH_STYLE", "true")
			t.Setenv("AWS_ENDPOINT_URL_S3", "http://s3.invalid")
			location := url.URL{Scheme: "s3", Host: "bucket", Path: "/prefix", User: test.userinfo}
			raw := location.String()
			if test.rawURL != "" {
				raw = test.rawURL
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			storage, err := Open(ctx, raw)
			if err != nil {
				t.Fatal(err)
			}
			var request *http.Request
			options := storage.client.Options()
			options.HTTPClient = httpClientFunc(func(r *http.Request) (*http.Response, error) {
				status, body := http.StatusOK, "saved content"
				if strings.HasSuffix(r.URL.Path, "/denied") {
					status = http.StatusForbidden
					body = fmt.Sprintf("<Error><Code>AccessDenied</Code><Message>%s</Message></Error>", test.secretKey)
				} else {
					request = r.Clone(r.Context())
				}
				return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})
			storage.client = s3.New(options)
			data, err := storage.Get(ctx, "object")
			if err != nil || string(data) != "saved content" {
				t.Fatalf("get object: %q, %v", data, err)
			}
			if request == nil {
				t.Fatal("S3 storage did not issue an HTTP request")
			}
			if request.Method != http.MethodGet || request.URL.Path != "/bucket/prefix/object" {
				t.Fatalf("unexpected S3 request: %s %s", request.Method, request.URL.Path)
			}
			if request.Header.Get("X-Amz-Security-Token") != test.sessionToken {
				t.Fatal("request used an unexpected session token")
			}
			verifyS3Signature(t, request, test.accessKey, test.secretKey)
			for name, want := range environment {
				if os.Getenv(name) != want {
					t.Fatalf("opening S3 storage changed %s", name)
				}
			}
			if _, err := storage.Get(ctx, "denied"); err == nil || strings.Contains(err.Error(), test.secretKey) || strings.Contains(err.Error(), raw) {
				t.Fatal("failed S3 request did not return a sanitized error")
			}
		})
	}
}

// Verify the actual request using its canonical signed headers, independently
// of the SDK signer, so a decoded key ID with the wrong secret cannot pass.
func verifyS3Signature(t *testing.T, request *http.Request, accessKey, secretKey string) {
	t.Helper()
	authorization, ok := strings.CutPrefix(request.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ")
	if !ok {
		t.Fatal("request was not signed with AWS Signature V4")
	}
	fields := map[string]string{}
	for _, field := range strings.Split(authorization, ",") {
		name, value, ok := strings.Cut(strings.TrimSpace(field), "=")
		if !ok {
			t.Fatal("invalid signature authorization fields")
		}
		fields[name] = value
	}
	scope, ok := strings.CutPrefix(fields["Credential"], accessKey+"/")
	if !ok {
		t.Fatal("request used an unexpected access key")
	}
	parts := strings.Split(scope, "/")
	if len(parts) != 4 || parts[1] != "us-east-1" || parts[2] != "s3" || parts[3] != "aws4_request" {
		t.Fatal("unexpected signature credential scope")
	}
	var headers strings.Builder
	for _, name := range strings.Split(fields["SignedHeaders"], ";") {
		value := request.Header.Get(name)
		if name == "host" {
			value = request.Host
			if value == "" {
				value = request.URL.Host
			}
		}
		headers.WriteString(name + ":" + strings.Join(strings.Fields(value), " ") + "\n")
	}
	payloadHash := request.Header.Get("X-Amz-Content-Sha256")
	if payloadHash == "" {
		empty := sha256.Sum256(nil)
		payloadHash = hex.EncodeToString(empty[:])
	}
	canonical := strings.Join([]string{request.Method, request.URL.EscapedPath(), request.URL.RawQuery, headers.String(), fields["SignedHeaders"], payloadHash}, "\n")
	digest := sha256.Sum256([]byte(canonical))
	toSign := "AWS4-HMAC-SHA256\n" + request.Header.Get("X-Amz-Date") + "\n" + scope + "\n" + hex.EncodeToString(digest[:])
	key := []byte("AWS4" + secretKey)
	for _, value := range parts {
		key = signatureHMAC(key, value)
	}
	if hex.EncodeToString(signatureHMAC(key, toSign)) != fields["Signature"] {
		t.Fatal("S3 request signature does not match the selected decoded credentials")
	}
}

func signatureHMAC(key []byte, value string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(value))
	return mac.Sum(nil)
}

type httpClientFunc func(*http.Request) (*http.Response, error)

func (f httpClientFunc) Do(request *http.Request) (*http.Response, error) { return f(request) }
