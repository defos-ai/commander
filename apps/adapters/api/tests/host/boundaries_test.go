package api_test

import (
	"context"
	"errors"
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode"

	"commander/adapters/api/endpoints"
	operationendpoints "commander/adapters/api/endpoints/operations"
	"commander/framework/comms/clients/controller/commands"
	"commander/framework/domain"
	"commander/framework/domain/artifacts"
	"commander/framework/domain/executions"
	"commander/framework/domain/operations"
)

// Transport failures and damaged metadata cannot be requested through the public
// API. Keep their HTTP boundary checks local, without starting another controller.
func TestCommandFailuresMapToPublicErrorsWithoutRetries(t *testing.T) {
	for _, tc := range []struct {
		code   string
		err    error
		status int
	}{
		{"operation_write_uncertain", errors.Join(context.DeadlineExceeded, operations.ErrWriteUncertain), 503},
		{"command_outcome_unknown", errors.Join(context.DeadlineExceeded, executions.ErrIndeterminate), 503},
		{"controller_unavailable", domain.ErrControllerClosed, 503},
		{"request_canceled", context.Canceled, 503},
		{"request_timeout", context.DeadlineExceeded, 504},
		{"internal_error", errors.New("private controller credentials and database path"), 500},
	} {
		t.Run(tc.code, func(t *testing.T) {
			command := &failedStart{err: tc.err}
			mux := http.NewServeMux()
			operationendpoints.Register(mux, nil, nil, command)
			request := httptest.NewRequest(http.MethodPost, "/api/v1/operations", strings.NewReader(`{"mission_name":"review-website","input":{}}`))
			request.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			mux.ServeHTTP(recorder, request)
			assertAPIError(t, response{status: recorder.Code, header: recorder.Header(), body: recorder.Body.String()}, tc.status, tc.code)
			if command.attempts != 1 || command.remaining <= 0 || command.remaining > 10*time.Second {
				t.Fatalf("command was retried or lost its bounded context: %+v", command)
			}
		})
	}
}

type failedStart struct {
	commands.Operations
	err       error
	attempts  int
	remaining time.Duration
}

func (s *failedStart) Start(ctx context.Context, _ commands.StartOperationCommand) (operations.Operation, error) {
	s.attempts++
	deadline, _ := ctx.Deadline()
	s.remaining = time.Until(deadline)
	return operations.Operation{}, s.err
}

func TestArtifactHeadersAndFileReadFailureBoundary(t *testing.T) {
	const artifactID = "00000000-0000-4000-8000-000000000001"
	const fileID = "00000000-0000-4000-8000-000000000002"
	metadata := artifactMetadata{value: artifacts.Artifact{ID: artifactID, Files: []artifacts.ArtifactFile{{ID: fileID, Name: "../../private\";\r\nX-Injected: yes\\file.bin", MediaType: "text/plain\r\nX-Injected: yes"}}}}
	for _, fail := range []bool{false, true} {
		files := &artifactBytes{}
		if fail {
			files.err = errors.New("private storage content failed integrity check")
		}
		mux := http.NewServeMux()
		endpoints.RegisterArtifacts(mux, metadata, files)
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/artifacts/"+artifactID+"/files/"+fileID, nil))
		result := response{status: recorder.Code, header: recorder.Header(), body: recorder.Body.String()}
		if fail {
			assertAPIError(t, result, http.StatusInternalServerError, "internal_error")
			if result.header.Get("Content-Disposition") != "" || result.header.Get("Content-Type") != "application/json" {
				t.Fatalf("failed read committed download headers: %v", result.header)
			}
			continue
		}
		kind, params, err := mime.ParseMediaType(result.header.Get("Content-Disposition"))
		if result.status != http.StatusOK || result.body != "" || result.header.Get("Content-Length") != "0" || result.header.Get("Content-Type") != "application/octet-stream" || result.header.Get("X-Injected") != "" {
			t.Fatalf("empty file or untrusted MIME damaged response: %+v", result)
		}
		if err != nil || kind != "attachment" || params["filename"] == "" || strings.ContainsAny(params["filename"], "/\\") || strings.IndexFunc(params["filename"], unicode.IsControl) >= 0 {
			t.Fatalf("unsafe download filename: %q", result.header.Get("Content-Disposition"))
		}
	}
}

type artifactMetadata struct {
	artifacts.ArtifactStore
	value artifacts.Artifact
}

func (s artifactMetadata) Get(context.Context, string) (artifacts.Artifact, error) {
	return s.value, nil
}

type artifactBytes struct {
	artifacts.ArtifactFileStore
	err error
}

func (s *artifactBytes) Get(context.Context, string, artifacts.ArtifactFile) ([]byte, error) {
	return []byte{}, s.err
}
