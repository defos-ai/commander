// Package missiontest drives missions through an existing Commander test API
// using explicit decisions instead of a model.
package missiontest

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/host"
)

type Config struct {
	URL              string
	Credentials      host.BasicCredentials
	Timeout          time.Duration
	PollInterval     time.Duration
	HTTPClient       *http.Client
	MaxArtifactBytes int64
}

func (Config) String() string   { return "Commander mission test configuration" }
func (Config) GoString() string { return "Commander mission test configuration" }

type Driver struct {
	t           testing.TB
	endpoint    string
	credentials host.BasicCredentials
	client      *http.Client
	timeout     time.Duration
	poll        time.Duration
	maxArtifact int64
}

func (*Driver) String() string   { return "Commander mission test driver" }
func (*Driver) GoString() string { return "Commander mission test driver" }

// Connect configures a driver for a test-owned stack. It does not start services.
func Connect(t testing.TB, config Config) *Driver {
	t.Helper()
	endpoint, err := url.Parse(config.URL)
	if err != nil || endpoint == nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") ||
		endpoint.Hostname() == "" || endpoint.User != nil || endpoint.RawQuery != "" ||
		endpoint.ForceQuery || endpoint.Fragment != "" || strings.Contains(config.URL, "#") || endpoint.Opaque != "" {
		t.Fatal("missiontest: URL must use HTTP or HTTPS without credentials, a query or a fragment")
	}
	if config.Credentials.Validate() != nil {
		t.Fatal("missiontest: API username and password must be set and valid")
	}
	if config.Timeout < 0 || config.PollInterval < 0 || config.MaxArtifactBytes < 0 || config.MaxArtifactBytes == 1<<63-1 {
		t.Fatal("missiontest: timeouts, polling interval and artifact limit must be positive or omitted")
	}
	if config.Timeout == 0 {
		config.Timeout = 2 * time.Minute
	}
	if config.PollInterval == 0 {
		config.PollInterval = 100 * time.Millisecond
	}
	if config.MaxArtifactBytes == 0 {
		config.MaxArtifactBytes = 32 << 20
	}
	client := http.Client{}
	if config.HTTPClient != nil {
		client = *config.HTTPClient
	}
	if client.Timeout <= 0 || client.Timeout > 10*time.Second {
		client.Timeout = 10 * time.Second
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Driver{
		t: t, endpoint: strings.TrimRight(config.URL, "/"), credentials: config.Credentials,
		client: &client, timeout: config.Timeout, poll: config.PollInterval, maxArtifact: config.MaxArtifactBytes,
	}
}
