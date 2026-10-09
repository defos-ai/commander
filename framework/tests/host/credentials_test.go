package host_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/defos-ai/commander/framework/host"
)

func TestCredentialsValidation(t *testing.T) {
	tests := []struct {
		name        string
		credentials host.Credentials
		valid       bool
		missing     bool
	}{
		{name: "basic", credentials: host.BasicCredentials{Username: "commander", Password: "secret:with:colons"}, valid: true},
		{name: "basic missing username", credentials: host.BasicCredentials{Password: "secret"}, missing: true},
		{name: "basic missing password", credentials: host.BasicCredentials{Username: "commander"}, missing: true},
		{name: "basic blank username", credentials: host.BasicCredentials{Username: " \t", Password: "secret"}, missing: true},
		{name: "basic blank password", credentials: host.BasicCredentials{Username: "commander", Password: " \t"}, missing: true},
		{name: "basic ambiguous username", credentials: host.BasicCredentials{Username: "commander:admin", Password: "secret"}},
		{name: "basic username control", credentials: host.BasicCredentials{Username: "commander\nadmin", Password: "secret"}},
		{name: "basic password control", credentials: host.BasicCredentials{Username: "commander", Password: "secret\x00value"}},
		{name: "bearer opaque token", credentials: host.BearerCredentials{Token: "abc.DEF-ghi_jkl~012+/"}, valid: true},
		{name: "bearer padded token", credentials: host.BearerCredentials{Token: "abc+/=="}, valid: true},
		{name: "bearer missing", credentials: host.BearerCredentials{}, missing: true},
		{name: "bearer blank", credentials: host.BearerCredentials{Token: " \t"}, missing: true},
		{name: "bearer leading space", credentials: host.BearerCredentials{Token: " secret"}},
		{name: "bearer trailing space", credentials: host.BearerCredentials{Token: "secret "}},
		{name: "bearer embedded space", credentials: host.BearerCredentials{Token: "secret value"}},
		{name: "bearer header injection", credentials: host.BearerCredentials{Token: "secret\r\nX-Test: injected"}},
		{name: "bearer unicode", credentials: host.BearerCredentials{Token: "secrét"}},
		{name: "bearer invalid punctuation", credentials: host.BearerCredentials{Token: "secret:value"}},
		{name: "bearer padding only", credentials: host.BearerCredentials{Token: "=="}},
		{name: "bearer embedded padding", credentials: host.BearerCredentials{Token: "ab=cd"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.credentials.Validate()
			if (err == nil) != tt.valid {
				t.Fatalf("Validate() error = %v, want valid = %t", err, tt.valid)
			}
			if errors.Is(err, host.ErrMissingCredentials) != tt.missing {
				t.Fatalf("Validate() error = %v, want missing = %t", err, tt.missing)
			}
			if err != nil && strings.Contains(err.Error(), "secret") {
				t.Fatal("validation error disclosed a credential value")
			}
		})
	}
}

func TestGeneratedSecretSupportsBasicAndBearerCredentials(t *testing.T) {
	secret, err := host.GeneratePassword()
	if err != nil {
		t.Fatal(err)
	}
	for _, credentials := range []host.Credentials{
		host.BasicCredentials{Username: "commander", Password: secret},
		host.BearerCredentials{Token: secret},
	} {
		if err := credentials.Validate(); err != nil {
			t.Fatalf("generated secret does not satisfy %T: %v", credentials, err)
		}
	}
}
