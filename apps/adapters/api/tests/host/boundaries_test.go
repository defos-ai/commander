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

	"github.com/defos-ai/commander/apps/adapters/api/endpoints"
	operationendpoints "github.com/defos-ai/commander/apps/adapters/api/endpoints/operations"
	"github.com/defos-ai/commander/apps/adapters/api/tests/fakes"
	hostfixtures "github.com/defos-ai/commander/apps/adapters/api/tests/fixtures/host"
	"github.com/defos-ai/commander/framework/domain"
	"github.com/defos-ai/commander/framework/domain/artifacts"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/domain/operations"
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
			command := fakes.NewFailedStart(tc.err)
			mux := http.NewServeMux()
			operationendpoints.Register(mux, nil, nil, nil, nil, command)
			request := httptest.NewRequest(http.MethodPost, "/api/v1/operations", strings.NewReader(`{"mission_name":"review-website-with-browser","input":{}}`))
			request.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()

			mux.ServeHTTP(recorder, request)

			assertAPIError(t, hostfixtures.Response{Status: recorder.Code, Header: recorder.Header(), Body: recorder.Body.String()}, tc.status, tc.code)
			calls := command.Snapshot()
			if calls.Attempts != 1 || calls.Remaining <= 0 || calls.Remaining > 10*time.Second {
				t.Fatalf("command was retried or lost its bounded context: %+v", command)
			}
		})
	}
}

func TestArtifactHeadersAndFileReadFailureBoundary(t *testing.T) {
	const artifactID = "00000000-0000-4000-8000-000000000001"
	const fileID = "00000000-0000-4000-8000-000000000002"
	metadata := fakes.NewArtifactMetadata(artifacts.Artifact{ID: artifactID, Files: []artifacts.ArtifactFile{{ID: fileID, Name: "../../private\";\r\nX-Injected: yes\\file.bin", MediaType: "text/plain\r\nX-Injected: yes"}}})
	for _, fail := range []bool{false, true} {
		var failure error
		if fail {
			failure = errors.New("private storage content failed integrity check")
		}
		files := fakes.NewArtifactBytes(failure)
		mux := http.NewServeMux()
		endpoints.RegisterArtifacts(mux, metadata, files)
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/api/v1/artifacts/"+artifactID+"/files/"+fileID, nil)

		mux.ServeHTTP(recorder, request)

		result := hostfixtures.Response{Status: recorder.Code, Header: recorder.Header(), Body: recorder.Body.String()}
		if fail {
			assertAPIError(t, result, http.StatusInternalServerError, "internal_error")
			if result.Header.Get("Content-Disposition") != "" || result.Header.Get("Content-Type") != "application/json" {
				t.Fatalf("failed read committed download headers: %v", result.Header)
			}
			continue
		}
		kind, params, err := mime.ParseMediaType(result.Header.Get("Content-Disposition"))
		if result.Status != http.StatusOK || result.Body != "" || result.Header.Get("Content-Length") != "0" || result.Header.Get("Content-Type") != "application/octet-stream" || result.Header.Get("X-Injected") != "" {
			t.Fatalf("empty file or untrusted MIME damaged response: %+v", result)
		}
		if err != nil || kind != "attachment" || params["filename"] == "" || strings.ContainsAny(params["filename"], "/\\") || strings.IndexFunc(params["filename"], unicode.IsControl) >= 0 {
			t.Fatalf("unsafe download filename: %q", result.Header.Get("Content-Disposition"))
		}
	}
}
