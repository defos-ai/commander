package missiontest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

const maxJSONBytes = 32 << 20

type statusError struct{ code int }

func (e *statusError) Error() string { return fmt.Sprintf("API returned HTTP %d", e.code) }

func hasStatus(err error, code int) bool {
	var status *statusError
	return errors.As(err, &status) && status.code == code
}

func (d *Driver) call(ctx context.Context, method, path string, input any, status int, output any) error {
	raw, err := d.exchange(ctx, method, path, input, status, maxJSONBytes)
	if err != nil {
		return err
	}
	if output != nil && json.Unmarshal(raw, output) != nil {
		return errors.New("API returned invalid JSON")
	}
	return nil
}

func (d *Driver) exchange(ctx context.Context, method, path string, input any, status int, limit int64) ([]byte, error) {
	var body io.Reader
	if input != nil {
		raw, err := json.Marshal(input)
		if err != nil {
			return nil, errors.New("input cannot be encoded as JSON")
		}
		body = bytes.NewReader(raw)
	}
	request, err := http.NewRequestWithContext(ctx, method, d.endpoint+path, body)
	if err != nil {
		return nil, errors.New("could not construct API request")
	}
	request.SetBasicAuth(d.credentials.Username, d.credentials.Password)
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := d.client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("API request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != status {
		return nil, &statusError{code: response.StatusCode}
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("could not read API response")
	}
	if int64(len(raw)) > limit {
		return nil, errors.New("API response exceeds the configured byte limit")
	}
	return raw, nil
}

func read[T any](d *Driver, method, path string, input any, status int) T {
	d.t.Helper()
	var result T
	if err := d.call(d.t.Context(), method, path, input, status, &result); err != nil {
		if method == http.MethodPost {
			d.t.Fatalf("missiontest: %s %s: %v; outcome may be unknown; read saved state before retrying", method, path, err)
		}
		d.t.Fatalf("missiontest: %s %s: %v", method, path, err)
	}
	return result
}
