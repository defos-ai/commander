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

const (
	maxJSONBytes  = 32 << 20
	maxErrorBytes = 4 << 10
)

type statusError struct {
	code   int
	reason string
}

func (e *statusError) Error() string {
	if e.reason != "" {
		return fmt.Sprintf("API returned HTTP %d (%s)", e.code, e.reason)
	}
	return fmt.Sprintf("API returned HTTP %d", e.code)
}

type unsentError struct{ message string }

func (e *unsentError) Error() string { return e.message }

func hasStatus(err error, code int) bool {
	var status *statusError
	return errors.As(err, &status) && status.code == code
}

func commandUncertain(err error) bool {
	var unsent *unsentError
	if errors.As(err, &unsent) {
		return false
	}
	var status *statusError
	if errors.As(err, &status) {
		switch status.reason {
		case "command_outcome_unknown", "operation_write_uncertain", "request_timeout", "request_canceled", "controller_unavailable", "internal_error":
			return true
		}
		switch status.code {
		case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden,
			http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusConflict,
			http.StatusRequestEntityTooLarge, http.StatusUnsupportedMediaType, http.StatusUnprocessableEntity:
			return false
		}
	}
	return true
}

func errorCode(body io.Reader) string {
	raw, err := io.ReadAll(io.LimitReader(body, maxErrorBytes+1))
	if err != nil || len(raw) > maxErrorBytes {
		return ""
	}
	var envelope struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return ""
	}
	// Remote text can contain credentials. Expose only known protocol codes.
	switch envelope.Error {
	case "invalid_request", "invalid_input", "invalid_output", "request_too_large", "unsupported_media_type",
		"mission_exists", "mission_not_found", "operation_not_found", "task_not_found", "question_not_found",
		"report_not_found", "artifact_not_found", "artifact_file_not_found", "session_not_found",
		"action_not_found", "agent_not_found", "unknown_controller", "invalid_operation_id", "invalid_task_id",
		"invalid_question_id", "invalid_artifact_id", "invalid_file_id", "invalid_cursor", "invalid_limit", "invalid_query",
		"operation_conflict", "operation_finished", "completion_operator_required", "operation_active_tasks",
		"operation_unresolved_work", "question_pending", "question_answered", "question_operator_waiting",
		"continuation_conflict", "task_limit_exceeded", "command_outcome_unknown", "operation_write_uncertain",
		"request_timeout", "request_canceled", "controller_unavailable", "internal_error":
		return envelope.Error
	}
	return ""
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
			return nil, &unsentError{message: "input cannot be encoded as JSON"}
		}
		body = bytes.NewReader(raw)
	}
	request, err := http.NewRequestWithContext(ctx, method, d.endpoint+path, body)
	if err != nil {
		return nil, &unsentError{message: "could not construct API request"}
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
		return nil, &statusError{code: response.StatusCode, reason: errorCode(response.Body)}
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
		if method == http.MethodPost && commandUncertain(err) {
			d.t.Fatalf("missiontest: %s %s: %v; outcome may be unknown; read saved state before retrying", method, path, err)
		}
		d.t.Fatalf("missiontest: %s %s: %v", method, path, err)
	}
	return result
}
