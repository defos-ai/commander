package host

import (
	"io"
	"net/http"
	"strings"
)

const (
	SecretTestKey         = "014ac583269fe047014ac583269fe047014ac583269fe047014ac583269fe047"
	SecretTestValue       = "host-test-private-token-original"
	SecretTestReplacement = "host-test-private-token-replaced"
	SecretsPath           = "/api/v1/secrets"
	SecretPath            = SecretsPath + "/GITHUB_TOKEN"
)

func SecretBody(body, contentType string) func(*http.Request) {
	return func(request *http.Request) {
		request.Body = io.NopCloser(strings.NewReader(body))
		request.ContentLength = int64(len(body))
		if contentType != "" {
			request.Header.Set("Content-Type", contentType)
		}
	}
}

func Authenticate(request *http.Request) {
	request.SetBasicAuth("api-fixture-user", "api-fixture-password")
}
