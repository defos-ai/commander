package environment_test

import (
	"strings"
	"testing"

	"commander/framework/agent/environment"
)

func TestHostInfoOverridePrecedence(t *testing.T) {
	for _, test := range []struct {
		name        string
		overrides   environment.HostInfo
		envIP       string
		envHostname string
		want        environment.HostInfo
	}{
		{
			name:      "explicit values override invalid environment",
			overrides: environment.HostInfo{IPAddress: "10.42.0.8", Hostname: "explicit-pod"},
			envIP:     "invalid", envHostname: " ",
			want: environment.HostInfo{IPAddress: "10.42.0.8", Hostname: "explicit-pod"},
		},
		{
			name:  "environment values are trimmed",
			envIP: " 2001:db8::8\t", envHostname: " env-pod\n",
			want: environment.HostInfo{IPAddress: "2001:db8::8", Hostname: "env-pod"},
		},
		{
			name:      "explicit IP allows hostname environment fallback",
			overrides: environment.HostInfo{IPAddress: "10.42.0.8"},
			envIP:     "invalid", envHostname: "env-pod",
			want: environment.HostInfo{IPAddress: "10.42.0.8", Hostname: "env-pod"},
		},
		{
			name:      "explicit hostname allows IP environment fallback",
			overrides: environment.HostInfo{Hostname: "explicit-pod"},
			envIP:     "10.42.0.9", envHostname: "invalid hostname",
			want: environment.HostInfo{IPAddress: "10.42.0.9", Hostname: "explicit-pod"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			t.Setenv("SERVER_IP_ADDRESS", test.envIP)
			t.Setenv("SERVER_HOSTNAME", test.envHostname)

			// Act.
			got, err := environment.ResolveHostInfo(test.overrides)

			// Assert.
			if err != nil || got != test.want {
				t.Fatalf("identity = %+v, error=%v, want %+v", got, err, test.want)
			}
		})
	}
}

func TestInvalidHostInfoOverridesAreRejected(t *testing.T) {
	for _, test := range []struct {
		name        string
		overrides   environment.HostInfo
		envIP       string
		envHostname string
		wantError   string
	}{
		{name: "invalid explicit IP", overrides: environment.HostInfo{IPAddress: "invalid"}, envIP: "10.42.0.8", wantError: "server IP address"},
		{name: "untrimmed explicit IP", overrides: environment.HostInfo{IPAddress: " 10.42.0.8 "}, wantError: "server IP address"},
		{name: "untrimmed explicit hostname", overrides: environment.HostInfo{Hostname: " pod "}, wantError: "server hostname"},
		{name: "invalid UTF-8 hostname", overrides: environment.HostInfo{Hostname: "\xff"}, wantError: "valid UTF-8"},
		{name: "invalid environment IP", envIP: "invalid", wantError: "SERVER_IP_ADDRESS"},
		{name: "blank environment IP", envIP: " \t", wantError: "SERVER_IP_ADDRESS"},
		{name: "blank environment hostname", envHostname: " \t", wantError: "SERVER_HOSTNAME"},
		{name: "invalid environment hostname", envHostname: "invalid hostname", wantError: "SERVER_HOSTNAME"},
		{name: "hostname path separator", envHostname: "pod/name", wantError: "SERVER_HOSTNAME"},
		{name: "hostname backslash", envHostname: `pod\name`, wantError: "SERVER_HOSTNAME"},
		{name: "hostname control character", envHostname: "pod\x07name", wantError: "SERVER_HOSTNAME"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			t.Setenv("SERVER_IP_ADDRESS", test.envIP)
			t.Setenv("SERVER_HOSTNAME", test.envHostname)

			// Act.
			got, err := environment.ResolveHostInfo(test.overrides)

			// Assert.
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error = %v, want %q", err, test.wantError)
			}
			if got != (environment.HostInfo{}) {
				t.Fatalf("failed resolution returned partial identity: %+v", got)
			}
		})
	}
}

func TestHostInfoValidationDoesNotResolveEnvironment(t *testing.T) {
	// Arrange.
	t.Setenv("SERVER_IP_ADDRESS", "invalid")
	t.Setenv("SERVER_HOSTNAME", "invalid hostname")
	info := environment.HostInfo{}

	// Act.
	err := info.Validate()

	// Assert.
	if err != nil {
		t.Fatalf("empty identity must remain valid regardless of environment: %v", err)
	}
}
