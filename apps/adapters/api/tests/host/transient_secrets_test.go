package api_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/apps/adapters/api/tests/fakes"
	hostfixtures "github.com/defos-ai/commander/apps/adapters/api/tests/fixtures/host"
	"github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/id"
)

func TestTransientSecretValueResumesItsExecutionThroughAuthenticatedAPI(t *testing.T) {
	stack := hostfixtures.NewTransientSecretStack(t)
	mission := models.CreateMissionRequest{Name: "verify-transient-code", Objectives: []string{"Verify one request-specific value."},
		RequiredActions: []string{fakes.TransientSecretActionName}, InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`)}
	decodeSecretResponse[models.MissionResponse](t, stack.API.Request(t, http.MethodPost, "/api/v1/missions", hostfixtures.Authenticate,
		hostfixtures.SecretBody(transientJSON(t, mission), "application/json")), http.StatusCreated)
	operation := decodeSecretResponse[models.OperationResponse](t, stack.API.Request(t, http.MethodPost, "/api/v1/operations", hostfixtures.Authenticate,
		hostfixtures.SecretBody(transientJSON(t, models.StartOperationRequest{MissionName: mission.Name, Input: json.RawMessage(`{}`)}), "application/json")), http.StatusCreated)
	task := decodeSecretResponse[models.TaskResponse](t, stack.API.Request(t, http.MethodPost, "/api/v1/operations/"+operation.ID+"/tasks", hostfixtures.Authenticate,
		hostfixtures.SecretBody(transientJSON(t, models.AssignTaskRequest{ActionName: fakes.TransientSecretActionName, Input: json.RawMessage(`{}`)}), "application/json")), http.StatusCreated)
	var request models.SecretRequestResponse
	stack.Await(t, func() bool {
		response := transientRequest(t, stack.API, http.MethodGet, "/api/v1/secret-requests?type=transient&agent_id="+stack.AgentID, "")
		pending := decodeSecretResponse[struct {
			Items []models.SecretRequestResponse `json:"items"`
		}](t, response, http.StatusOK)
		if len(pending.Items) == 0 {
			return false
		}
		if len(pending.Items) != 1 {
			t.Fatal("one execution created multiple transient requests")
		}
		request = pending.Items[0]
		return true
	})
	if request.Type != "transient" || request.Name != "CODE" || request.Status != "pending" || request.OperationID != operation.ID || request.TaskID != task.ID || request.CommandID == "" {
		t.Fatal("pending transient request lost its execution scope or type")
	}
	path := "/api/v1/secret-requests/" + request.ID + "/values"
	body := transientJSON(t, map[string]string{"value": fakes.TransientSecretValue})

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		target := path
		if method == http.MethodGet {
			target = "/api/v1/secret-requests"
		}
		response := stack.API.Request(t, method, target, hostfixtures.SecretBody(body, "application/json"))
		if response.Status != http.StatusUnauthorized {
			t.Fatal("transient requests accepted an unauthenticated caller")
		}
		assertNoTransientResponse(t, response)
	}
	for _, invalid := range []struct {
		path, body, media string
		status            int
	}{
		{path, `{}`, "application/json", http.StatusBadRequest},
		{path, `{"value":null}`, "application/json", http.StatusBadRequest},
		{path, `{"value":false}`, "application/json", http.StatusBadRequest},
		{path, `{"value":"private-value","unexpected":"private-query-value"}`, "application/json", http.StatusBadRequest},
		{path, `{"value":`, "application/json", http.StatusBadRequest},
		{path, "{\"value\":\"\xff\"}", "application/json", http.StatusBadRequest},
		{path, body, "text/plain", http.StatusUnsupportedMediaType},
		{path + "?unexpected=private-query-value", body, "application/json", http.StatusBadRequest},
		{"/api/v1/secret-requests/private-invalid-id/values", body, "application/json", http.StatusBadRequest},
		{path, transientJSON(t, map[string]string{"value": strings.Repeat("x", secrets.MaxTransientSecretBytes+1)}), "application/json", http.StatusBadRequest},
	} {
		response := stack.API.Request(t, http.MethodPost, invalid.path, hostfixtures.Authenticate, hostfixtures.SecretBody(invalid.body, invalid.media))
		if response.Status != invalid.status {
			t.Fatalf("invalid transient submission returned %d, want %d", response.Status, invalid.status)
		}
		assertNoTransientResponse(t, response)
	}
	if stack.Bus.HeldCount() != 0 || stack.Action.Calls() != 1 {
		t.Fatal("rejected input delivered a value or repeated execution")
	}
	reusable := decodeSecretResponse[struct {
		Items []models.SecretRequestResponse `json:"items"`
	}](t,
		transientRequest(t, stack.API, http.MethodGet, "/api/v1/secret-requests?type=reusable", ""), http.StatusOK)
	if len(reusable.Items) != 0 {
		t.Fatal("transient request entered the reusable-only form")
	}
	for _, closed := range []struct {
		kind   secrets.SecretType
		status secrets.RequestStatus
	}{
		{secrets.SecretReusable, secrets.Pending}, {secrets.SecretTransient, secrets.Cancelled}, {secrets.SecretTransient, secrets.RequestExpired},
	} {
		saved := stack.RetainRequest(t, closed.kind, closed.status)
		response := transientRequest(t, stack.API, http.MethodPost, "/api/v1/secret-requests/"+saved.ID+"/values", body)
		if response.Status != http.StatusConflict {
			t.Fatal("reusable or closed request accepted a transient value")
		}
		assertNoTransientResponse(t, response)
	}
	missing := transientRequest(t, stack.API, http.MethodPost, "/api/v1/secret-requests/"+id.New()+"/values", body)
	if missing.Status != http.StatusNotFound {
		t.Fatal("unknown transient request was not rejected")
	}

	stack.Bus.FailNextTransientPublication()
	uncertain := transientRequest(t, stack.API, http.MethodPost, path, body)
	if uncertain.Status != http.StatusServiceUnavailable || !strings.Contains(uncertain.Body, "secret_delivery_uncertain") {
		t.Fatal("failed publication claimed acceptance or definite rejection")
	}
	if stack.Bus.HeldCount() > 1 || stack.Action.Calls() != 1 {
		t.Fatal("uncertain submission duplicated delivery or execution")
	}
	for range 2 {
		accepted := decodeSecretResponse[struct {
			Status string `json:"status"`
		}](t, transientRequest(t, stack.API, http.MethodPost, path, body), http.StatusAccepted)
		if accepted.Status != "awaiting_receipt" {
			t.Fatal("accepted transient value claimed receipt or successful use")
		}
	}
	changed := transientRequest(t, stack.API, http.MethodPost, path, `{"value":"different-private-code"}`)
	if changed.Status != http.StatusConflict || stack.Bus.HeldCount() != 1 || stack.Action.Calls() != 1 {
		t.Fatal("changed or repeated submission replaced the delivery or repeated execution")
	}
	stack.AssertNoStoredValue(t)
	if err := stack.Bus.Release(stack.Context); err != nil {
		t.Fatal("could not deliver accepted transient value")
	}
	stack.Await(t, func() bool {
		response := transientRequest(t, stack.API, http.MethodGet, "/api/v1/operations/"+operation.ID+"/tasks/"+task.ID, "")
		current := decodeSecretResponse[models.TaskResponse](t, response, http.StatusOK)
		if current.Status == "failed" || current.Status == "interrupted" || current.Status == "cancelled" {
			t.Fatal("transient submission failed its original execution")
		}
		if current.Status != "succeeded" {
			return false
		}
		if current.Result == nil || !bytes.Equal(current.Result.Output, []byte(`{"matched":true}`)) || current.CommandID != request.CommandID {
			t.Fatal("transient submission resumed a different execution or returned its value")
		}
		return true
	})
	stack.Await(t, func() bool {
		response := transientRequest(t, stack.API, http.MethodGet, "/api/v1/secret-requests/"+request.ID, "")
		saved := decodeSecretResponse[models.SecretRequestResponse](t, response, http.StatusOK)
		if saved.Status == "cancelled" || saved.Status == "expired" {
			t.Fatalf("transient request %s before its delivery receipt was saved", saved.Status)
		}
		if saved.Status != "fulfilled" {
			return false
		}
		if saved.OperationID != request.OperationID || saved.TaskID != request.TaskID || saved.CommandID != request.CommandID || saved.Type != "transient" {
			t.Fatal("terminal request readback changed execution provenance")
		}
		return true
	})
	saved, err := stack.Requests.Get(stack.Context, request.ID)
	if err != nil || saved.Request.Fulfillment == nil || saved.Request.Fulfillment.DeliveryID == "" || saved.Request.Fulfillment.SecretID != "" || saved.Delivery != nil || saved.Installation != nil {
		t.Fatal("transient receipt retained reusable or value-bearing data")
	}
	closed := transientRequest(t, stack.API, http.MethodPost, path, body)
	if closed.Status != http.StatusConflict || stack.Action.Calls() != 1 {
		t.Fatal("fulfilled transient request reopened execution")
	}
	stack.AssertNoStoredValue(t)
}

func transientJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func assertNoTransientResponse(t *testing.T, response hostfixtures.Response) {
	t.Helper()
	assertNoSecretResponse(t, response)
	for _, private := range []string{fakes.TransientSecretValue, "different-private-code", base64.StdEncoding.EncodeToString([]byte(fakes.TransientSecretValue))} {
		if strings.Contains(response.Body, private) {
			t.Fatal("API response exposed a transient value")
		}
	}
	if response.Header.Get("Cache-Control") != "no-store" && response.Status != http.StatusUnauthorized {
		t.Fatalf("transient response must disable caching: %d", response.Status)
	}
}

func transientRequest(t *testing.T, server *hostfixtures.API, method, path, body string) hostfixtures.Response {
	t.Helper()
	response := secretRequest(t, server, method, path, body)
	assertNoTransientResponse(t, response)
	return response
}
