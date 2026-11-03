package agent_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/defos-ai/commander/framework/agent/action"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/agent"
)

func TestSecretRequestOptionsProtectDefaultAndReturnedValues(t *testing.T) {
	defaultValue := []byte("default-value-sentinel")
	options := action.SecretRequestOptions{Default: defaultValue}
	encoded, err := json.Marshal(options)
	if err != nil {
		t.Fatal(err)
	}
	schema, err := action.SchemaFor[action.SecretRequestOptions]()
	if err != nil {
		t.Fatal(err)
	}
	for _, diagnostic := range []string{fmt.Sprint(options), fmt.Sprintf("%+v", options), fmt.Sprintf("%#v", options), string(encoded), string(schema)} {
		if strings.Contains(diagnostic, string(defaultValue)) || strings.Contains(diagnostic, fmt.Sprint(defaultValue)) ||
			strings.Contains(diagnostic, base64.StdEncoding.EncodeToString(defaultValue)) {
			t.Fatal("default value escaped into formatting or serialization")
		}
	}
	var properties struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(schema, &properties); err != nil {
		t.Fatal(err)
	}
	if _, exposed := properties.Properties["Default"]; exposed {
		t.Fatal("secret default became an action schema field")
	}
	if _, exposed := properties.Properties["default"]; exposed {
		t.Fatal("secret default became an action schema field")
	}
	var types []action.SecretType
	var forwardedDefault []byte
	reusableValue, transientValue := []byte("reusable-value"), []byte("transient-value")
	provider := fakes.SecretOptionsRequesterFunc(func(_ context.Context, name string, received action.SecretRequestOptions) ([]byte, error) {
		types = append(types, received.Type)
		if name == "TOKEN" {
			forwardedDefault = received.Default
			if !bytes.Equal(forwardedDefault, defaultValue) {
				return nil, errors.New("default changed before provider call")
			}
			forwardedDefault[0] = 'X'
			return reusableValue, nil
		}
		return transientValue, nil
	})
	ctx := action.WithSecrets(t.Context(), provider)

	reusable, err := action.RequestReusableSecret(ctx, "TOKEN", options)
	if err != nil {
		t.Fatal(err)
	}
	transient, err := action.RequestSecret(ctx, "CODE", action.SecretRequestOptions{Type: action.SecretTransient})
	if err != nil {
		t.Fatal(err)
	}
	defer clear(reusable)
	defer clear(transient)

	if !slices.Equal(types, []action.SecretType{action.SecretReusable, action.SecretTransient}) || string(reusable) != "reusable-value" || string(transient) != "transient-value" {
		t.Fatal("typed provider did not receive the selected request types or return independent values")
	}
	if string(defaultValue) != "default-value-sentinel" || !bytes.Equal(forwardedDefault, make([]byte, len(forwardedDefault))) ||
		!bytes.Equal(reusableValue, make([]byte, len(reusableValue))) || !bytes.Equal(transientValue, make([]byte, len(transientValue))) {
		t.Fatal("request helper changed caller bytes or retained provider-owned plaintext")
	}
}

func TestSecretRequestOptionsCannotSilentlyFallBackToReusableRequests(t *testing.T) {
	calls := 0
	provider := fakes.SecretRequesterFunc(func(context.Context, string) ([]byte, error) {
		calls++
		return []byte("stored-value"), nil
	})
	ctx := action.WithSecrets(t.Context(), provider)
	for _, scenario := range []struct {
		name        string
		request     func() ([]byte, error)
		unsupported bool
	}{
		{"transient", func() ([]byte, error) { return action.RequestTransientSecret(ctx, "TOKEN") }, true},
		{"explicit transient", func() ([]byte, error) {
			return action.RequestSecret(ctx, "TOKEN", action.SecretRequestOptions{Type: action.SecretTransient})
		}, true},
		{"default", func() ([]byte, error) {
			return action.RequestReusableSecret(ctx, "TOKEN", action.SecretRequestOptions{Default: []byte("default-sentinel")})
		}, true},
		{"empty default", func() ([]byte, error) {
			return action.RequestSecret(ctx, "TOKEN", action.SecretRequestOptions{Default: []byte{}})
		}, true},
		{"unknown type", func() ([]byte, error) {
			return action.RequestSecret(ctx, "TOKEN", action.SecretRequestOptions{Type: "unsupported"})
		}, false},
		{"multiple options", func() ([]byte, error) {
			return action.RequestSecret(ctx, "TOKEN", action.SecretRequestOptions{}, action.SecretRequestOptions{})
		}, false},
		{"reusable alias conflict", func() ([]byte, error) {
			return action.RequestReusableSecret(ctx, "TOKEN", action.SecretRequestOptions{Type: action.SecretTransient})
		}, false},
		{"transient alias conflict", func() ([]byte, error) {
			return action.RequestTransientSecret(ctx, "TOKEN", action.SecretRequestOptions{Type: action.SecretReusable})
		}, false},
		{"transient default", func() ([]byte, error) {
			return action.RequestTransientSecret(ctx, "TOKEN", action.SecretRequestOptions{Default: []byte{}})
		}, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			value, err := scenario.request()
			defer clear(value)

			if err == nil || value != nil || errors.Is(err, action.ErrSecretRequestUnsupported) != scenario.unsupported || calls != 0 {
				t.Fatal("invalid or unsupported request reached the reusable provider or lost its error")
			}
		})
	}

	value, err := action.RequestSecret(ctx, "TOKEN")
	defer clear(value)
	if err != nil || string(value) != "stored-value" || calls != 1 {
		t.Fatal("existing two-argument requests stopped supporting legacy providers")
	}
	value, err = action.RequestReusableSecret(ctx, "TOKEN", action.SecretRequestOptions{Type: action.SecretReusable})
	defer clear(value)
	if err != nil || string(value) != "stored-value" || calls != 2 {
		t.Fatal("reusable alias stopped supporting legacy providers")
	}
}

func TestSecretRequestProviderErrorsClearValuesAndPreserveCancellation(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		err    error
		cancel bool
		want   error
	}{
		{"provider error", errors.New("private-provider-error-sentinel"), false, action.ErrSecretUnavailable},
		{"unsupported", fmt.Errorf("unsupported: %w", action.ErrSecretRequestUnsupported), false, action.ErrSecretRequestUnsupported},
		{"deadline", fmt.Errorf("provider: %w", context.DeadlineExceeded), false, context.DeadlineExceeded},
		{"cancellation", nil, true, context.Canceled},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			defaultValue, returned := []byte("default-sentinel"), []byte("returned-sentinel")
			var forwarded []byte
			provider := fakes.SecretOptionsRequesterFunc(func(_ context.Context, _ string, options action.SecretRequestOptions) ([]byte, error) {
				forwarded = options.Default
				if scenario.cancel {
					cancel()
				}
				return returned, scenario.err
			})
			ctx = action.WithSecrets(ctx, provider)

			value, err := action.RequestReusableSecret(ctx, "TOKEN", action.SecretRequestOptions{Default: defaultValue})
			defer clear(value)

			if value != nil || !errors.Is(err, scenario.want) || strings.Contains(err.Error(), "sentinel") {
				t.Fatal("provider failure leaked a value or changed its public cancellation/error contract")
			}
			if string(defaultValue) != "default-sentinel" || !bytes.Equal(forwarded, make([]byte, len(forwarded))) || !bytes.Equal(returned, make([]byte, len(returned))) {
				t.Fatal("failed request changed caller bytes or retained its plaintext copies")
			}
		})
	}
}
