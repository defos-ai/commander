package tests

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/defos-ai/commander/apps/admin/client"
	adminsessions "github.com/defos-ai/commander/apps/admin/sessions"
	"github.com/defos-ai/commander/apps/admin/tests/fixtures"
	"github.com/defos-ai/commander/framework/id"
)

func TestSessionControlsPreserveUncertainOutcomesWithoutRetrying(t *testing.T) {
	const marker = "private-server-value"
	valid := fixtures.ControlSessionJSON()
	for _, tc := range []struct {
		name      string
		status    int
		body      string
		lostReply bool
		generated bool
		uncertain bool
	}{
		{name: "unconfirmed close", status: 503, body: `{"error":"session_control_uncertain"}`, uncertain: true},
		{name: "lost acknowledgement", lostReply: true, generated: true, uncertain: true},
		{name: "different session", status: 200, body: strings.Replace(valid, fixtures.ControlSessionID, id.New(), 1), uncertain: true},
		{name: "different scope", status: 200, body: strings.Replace(valid, fixtures.ControlScopeID, id.New(), 1), uncertain: true},
		{name: "different owner", status: 200, body: strings.Replace(valid, fixtures.ControlOwnerID, "fixture-agent-00000002", 1), uncertain: true},
		{name: "invalid observation", status: 200, body: strings.Replace(valid, "connected", marker, 1), uncertain: true},
		{name: "unexpected success status", status: 201, body: valid, uncertain: true},
		{name: "rejected close may remain pending", status: 422, body: `{"error":"` + marker + `"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var reads, writes atomic.Int32
			var submitted atomic.Value
			submitted.Store("")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodGet && r.URL.Path == "/api/v1/sessions/"+fixtures.ControlSessionID {
					reads.Add(1)
					_, _ = io.WriteString(w, valid)
					return
				}
				if r.Method != http.MethodPost || r.URL.Path != "/api/v1/sessions/control" {
					t.Errorf("unexpected session request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
					return
				}
				writes.Add(1)
				var request map[string]string
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				if request["id"] != fixtures.ControlSessionID || request["scope_id"] != fixtures.ControlScopeID || request["owner_agent_id"] != fixtures.ControlOwnerID || request["method"] != "close" || !id.Valid(request["request_id"]) {
					t.Errorf("control did not retain the resolved identity: %+v", request)
				}
				submitted.Store(request["request_id"])
				if tc.lostReply {
					connection, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					_ = connection.Close()
					return
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			requestID := fixtures.ControlRequestID
			if tc.generated {
				requestID = ""
			}
			_, err := adminsessions.Control(t.Context(), fixtures.NewClient(t, server.URL), fixtures.ControlSessionID, "close", requestID)
			var controlError *adminsessions.ControlError
			sentID := submitted.Load().(string)
			if !errors.As(err, &controlError) || controlError.RequestID != sentID || !id.Valid(sentID) || reads.Load() != 1 || writes.Load() != 1 {
				t.Fatalf("control was retried or lost its request identity: error=%v reads=%d writes=%d", err, reads.Load(), writes.Load())
			}
			if !tc.generated && sentID != requestID {
				t.Fatal("explicit request identity changed")
			}
			if errors.Is(err, client.ErrWriteUncertain) != tc.uncertain || !strings.Contains(err.Error(), sentID) || !strings.Contains(strings.ToLower(err.Error()), "inspect") {
				t.Fatalf("control lost uncertainty or recovery guidance: %v", err)
			}
			if tc.status == http.StatusUnprocessableEntity {
				var status *client.StatusError
				if !errors.As(err, &status) || status.StatusCode != tc.status {
					t.Fatalf("control lost its rejected status: %v", err)
				}
			}
			for _, secret := range []string{marker, "private-password", server.URL} {
				if strings.Contains(err.Error(), secret) {
					t.Fatal("control diagnostics exposed private server data or connection settings")
				}
			}
		})
	}
}

func TestSessionControlsRequireConfirmedOwnershipBeforeMutation(t *testing.T) {
	valid := fixtures.ControlSessionJSON()
	for _, tc := range []struct {
		name     string
		status   int
		body     string
		notFound bool
	}{
		{"missing session", 404, `{"error":"session_not_found"}`, true},
		{"unrelated missing route", 404, `{"error":"route_not_found"}`, false},
		{"wrong lookup identity", 200, strings.Replace(valid, fixtures.ControlSessionID, id.New(), 1), false},
		{"unassigned session", 200, strings.Replace(valid, fixtures.ControlOwnerID, "", 1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var reads, writes atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					writes.Add(1)
				} else {
					reads.Add(1)
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			_, err := adminsessions.Control(t.Context(), fixtures.NewClient(t, server.URL), fixtures.ControlSessionID, "close", fixtures.ControlRequestID)
			var controlError *adminsessions.ControlError
			if err == nil || reads.Load() != 1 || writes.Load() != 0 || errors.Is(err, client.ErrWriteUncertain) || errors.As(err, &controlError) || errors.Is(err, adminsessions.ErrNotFound) != tc.notFound {
				t.Fatalf("unconfirmed ownership reached a mutation or gained false certainty: error=%v reads=%d writes=%d", err, reads.Load(), writes.Load())
			}
		})
	}
}
