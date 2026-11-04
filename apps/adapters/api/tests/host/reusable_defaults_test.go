package api_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/apps/adapters/api/tests/fakes"
	hostfixtures "github.com/defos-ai/commander/apps/adapters/api/tests/fixtures/host"
	"github.com/defos-ai/commander/framework/domain/secrets"
)

func TestReusableDefaultBecomesSharedEncryptedSecretForLaterAgents(t *testing.T) {
	stack := hostfixtures.NewTransientSecretStack(t)
	var agentLogs []*fakes.SecretLogs
	t.Cleanup(func() {
		for _, logs := range agentLogs {
			assertNoReusableDefault(t, logs.Snapshot())
		}
	})
	first := fakes.NewReusableSecretAction("create-reusable-login", []byte(fakes.ReusableSecretDefault), []byte(fakes.ReusableSecretDefault))
	firstAgent, firstLogs := stack.StartReusableSecretAgent(t, "login-first", first)
	agentLogs = append(agentLogs, firstLogs)
	mission := models.CreateMissionRequest{Name: "share-reusable-login", Objectives: []string{"Reuse a supplied credential without another prompt."},
		RequiredActions: []string{first.Action().Name}, InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`)}
	decodeSecretResponse[models.MissionResponse](t, stack.API.Request(t, http.MethodPost, "/api/v1/missions", hostfixtures.Authenticate,
		hostfixtures.SecretBody(transientJSON(t, mission), "application/json")), http.StatusCreated)
	operation := decodeSecretResponse[models.OperationResponse](t, stack.API.Request(t, http.MethodPost, "/api/v1/operations", hostfixtures.Authenticate,
		hostfixtures.SecretBody(transientJSON(t, models.StartOperationRequest{MissionName: mission.Name, Input: json.RawMessage(`{}`)}), "application/json")), http.StatusCreated)

	firstTask := decodeSecretResponse[models.TaskResponse](t, stack.API.Request(t, http.MethodPost, "/api/v1/operations/"+operation.ID+"/tasks", hostfixtures.Authenticate,
		hostfixtures.SecretBody(transientJSON(t, models.AssignTaskRequest{ActionName: first.Action().Name, Input: json.RawMessage(`{}`)}), "application/json")), http.StatusCreated)
	awaitReusableSecretTask(t, stack, operation.ID, firstTask.ID, firstAgent)

	saved, err := stack.Secrets.Get(stack.Context, secrets.Key{Name: fakes.ReusableSecretName})
	if err != nil || saved.Metadata.Version != 1 {
		t.Fatal("default was not saved as one shared secret")
	}
	if bytes.Contains(saved.Value.Ciphertext, []byte(fakes.ReusableSecretDefault)) {
		t.Fatal("managed secret retained an unencrypted default")
	}
	metadataResponse := stack.API.Request(t, http.MethodGet, "/api/v1/secrets/"+fakes.ReusableSecretName, hostfixtures.Authenticate)
	metadata := decodeSecretResponse[models.SecretResponse](t, metadataResponse, http.StatusOK)
	assertNoReusableDefault(t, metadataResponse.Body)
	if metadata.ID != saved.Metadata.ID || metadata.Version != saved.Metadata.Version || metadata.Status != "available" {
		t.Fatal("API did not expose the saved shared-secret metadata")
	}

	later := fakes.NewReusableSecretAction("use-reusable-login", nil, []byte(fakes.ReusableSecretDefault))
	laterAgent, laterLogs := stack.StartReusableSecretAgent(t, "login-later", later)
	agentLogs = append(agentLogs, laterLogs)
	laterTask := decodeSecretResponse[models.TaskResponse](t, stack.API.Request(t, http.MethodPost, "/api/v1/operations/"+operation.ID+"/tasks", hostfixtures.Authenticate,
		hostfixtures.SecretBody(transientJSON(t, models.AssignTaskRequest{ActionName: later.Action().Name, Input: json.RawMessage(`{}`)}), "application/json")), http.StatusCreated)
	awaitReusableSecretTask(t, stack, operation.ID, laterTask.ID, laterAgent)

	competing := fakes.NewReusableSecretAction("preserve-reusable-login", []byte(fakes.ReusableSecretOtherDefault), []byte(fakes.ReusableSecretDefault))
	competingAgent, competingLogs := stack.StartReusableSecretAgent(t, "login-competing", competing)
	agentLogs = append(agentLogs, competingLogs)
	competingTask := decodeSecretResponse[models.TaskResponse](t, stack.API.Request(t, http.MethodPost, "/api/v1/operations/"+operation.ID+"/tasks", hostfixtures.Authenticate,
		hostfixtures.SecretBody(transientJSON(t, models.AssignTaskRequest{ActionName: competing.Action().Name, Input: json.RawMessage(`{}`)}), "application/json")), http.StatusCreated)
	awaitReusableSecretTask(t, stack, operation.ID, competingTask.ID, competingAgent)

	after, err := stack.Secrets.Get(stack.Context, saved.Metadata.Key)
	if err != nil || !reflect.DeepEqual(after, saved) || first.Calls() != 1 || later.Calls() != 1 || competing.Calls() != 1 {
		t.Fatal("later agents replaced the saved default or repeated execution")
	}
	stack.AssertNoStoredReusablePlaintext(t, fakes.ReusableSecretDefault, fakes.ReusableSecretOtherDefault)
	requests := stack.API.Request(t, http.MethodGet, "/api/v1/secret-requests", hostfixtures.Authenticate)
	if requests.Status != http.StatusOK {
		t.Fatal("shared request metadata could not be read")
	}
	assertNoReusableDefault(t, requests.Body)
}

func awaitReusableSecretTask(t *testing.T, stack *hostfixtures.TransientSecretStack, operationID, taskID, agentID string) {
	t.Helper()
	stack.Await(t, func() bool {
		response := stack.API.Request(t, http.MethodGet, "/api/v1/operations/"+operationID+"/tasks/"+taskID, hostfixtures.Authenticate)
		assertNoReusableDefault(t, response.Body)
		current := decodeSecretResponse[models.TaskResponse](t, response, http.StatusOK)
		if current.Status == "failed" || current.Status == "interrupted" || current.Status == "cancelled" {
			t.Fatalf("reusable secret execution ended: agent=%s status=%s error=%s", current.AgentID, current.Status, current.Error)
		}
		if current.Status != "succeeded" {
			return false
		}
		if current.AgentID != agentID || current.Result == nil || !bytes.Equal(current.Result.Output, []byte(`{"matched":true}`)) {
			t.Fatal("agent did not receive the expected shared reusable value")
		}
		return true
	})
}

func assertNoReusableDefault(t *testing.T, text string) {
	t.Helper()
	for _, value := range []string{fakes.ReusableSecretDefault, fakes.ReusableSecretOtherDefault} {
		if strings.Contains(text, value) || strings.Contains(text, base64.StdEncoding.EncodeToString([]byte(value))) {
			t.Fatal("response or log exposed a reusable default value")
		}
	}
}
