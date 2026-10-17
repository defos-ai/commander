package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/crypto"
	secretmodel "github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/id"
	fssecrets "github.com/defos-ai/commander/providers/fs/secrets"
)

const secretRequestsPath = "/api/v1/secrets/requests"

type pendingSecretView struct {
	ID          string    `json:"id"`
	AgentType   string    `json:"agent_type"`
	Name        string    `json:"name"`
	AgentID     string    `json:"agent_id"`
	OperationID string    `json:"operation_id,omitempty"`
	RequestedAt time.Time `json:"requested_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	Status      string    `json:"status"`
}

func TestPendingSecretRequestsFollowValueDeliveryAndInstallation(t *testing.T) {
	fixture := newFixture(t)
	t.Setenv("SECRET_ENCRYPTION_KEY", secretTestKey)
	requests, values := secretRequestStores(t, fixture)
	request := pendingSecretRequest("builder-agent", "GITHUB_TOKEN", "builder-agent-00000001")
	request.OperationID = id.New()
	if err := requests.Create(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	server := startAPI(t, fixture.localArgs())
	assertStatus := func(want string) {
		t.Helper()
		items := listSecretRequests(t, server, "")
		if len(items) != 1 {
			t.Fatalf("pending inventory has %d requests, want one", len(items))
		}
		expected := pendingSecretView{
			ID: request.ID, AgentType: request.Key.Scope.AgentType, Name: request.Key.Name,
			AgentID: request.AgentID, OperationID: request.OperationID,
			RequestedAt: request.RequestedAt, ExpiresAt: request.ExpiresAt, Status: want,
		}
		if !reflect.DeepEqual(items[0], expected) {
			t.Fatalf("pending metadata = %+v, want %+v", items[0], expected)
		}
	}
	assertStatus("needs_value")
	createBody := `{"agent_type":"builder-agent","name":"GITHUB_TOKEN","value":"` + secretTestValue + `"}`
	created := decodeSecretResponse[secretMetadata](t, secretRequest(t, server, http.MethodPost, secretsPath, createBody), http.StatusCreated)
	assertStatus("awaiting_delivery")
	reserveSecretDelivery(t, requests, values, request)
	assertStatus("awaiting_installation")
	// A reservation is not an acknowledgement, and reads must not settle it.
	unchanged, err := requests.Get(t.Context(), request.ID)
	if err != nil || unchanged.Request.Status != secretmodel.Pending || unchanged.Installation != nil {
		t.Fatal("reading a reserved delivery claimed installation")
	}
	replaced := decodeSecretResponse[secretMetadata](t, secretRequest(t, server, http.MethodPut, secretPath,
		fmt.Sprintf(`{"id":%q,"version":1,"value":%q}`, created.ID, secretTestReplacement)), http.StatusOK)
	assertStatus("awaiting_delivery")
	deleted := secretRequest(t, server, http.MethodDelete,
		fmt.Sprintf("%s?id=%s&version=%d", secretPath, replaced.ID, replaced.Version), "")
	if deleted.status != http.StatusNoContent {
		t.Fatal("could not remove test secret")
	}
	assertStatus("needs_value")
	recreated := decodeSecretResponse[secretMetadata](t, secretRequest(t, server, http.MethodPost, secretsPath, createBody), http.StatusCreated)
	if recreated.ID == created.ID || recreated.Version != 1 {
		t.Fatal("recreation did not allocate a new identity")
	}
	assertStatus("awaiting_delivery")
	if err := requests.Cancel(t.Context(), request.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	request = pendingSecretRequest("builder-agent", "GITHUB_TOKEN", request.AgentID)
	if err := requests.Create(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	delivery := reserveSecretDelivery(t, requests, values, request)
	assertStatus("awaiting_installation")
	installedAt := time.Now().UTC()
	if err := requests.Acknowledge(t.Context(), secretmodel.Installation{
		ID: id.New(), RequestID: request.ID, DeliveryID: delivery.ID, AgentID: request.AgentID,
		SecretID: delivery.Secret.Metadata.ID, SecretVersion: delivery.Secret.Metadata.Version,
		InstalledAt: installedAt,
	}, installedAt); err != nil {
		t.Fatal(err)
	}
	if items := listSecretRequests(t, server, ""); len(items) != 0 {
		t.Fatal("settled requests remained in the pending inventory")
	}
}

func TestPendingSecretRequestsFilterAndExcludeClosedOrOverdueWithoutMutation(t *testing.T) {
	fixture := newFixture(t)
	t.Setenv("SECRET_ENCRYPTION_KEY", secretTestKey)
	requests, _ := secretRequestStores(t, fixture)
	one := pendingSecretRequest("builder-agent", "GITHUB_TOKEN", "builder-agent-00000001")
	two := pendingSecretRequest("builder-agent", "BUILD_TOKEN", "builder-agent-00000002")
	three := pendingSecretRequest("web-agent", "SERVICE_TOKEN", "web-agent-00000001")
	cancelled := pendingSecretRequest("builder-agent", "CANCELLED_TOKEN", one.AgentID)
	overdue := pendingSecretRequest("builder-agent", "OVERDUE_TOKEN", one.AgentID)
	overdue.ExpiresAt = time.Now().UTC().Add(-time.Minute)
	expired := pendingSecretRequest("builder-agent", "EXPIRED_TOKEN", one.AgentID)
	expired.ExpiresAt = overdue.ExpiresAt
	for _, request := range []secretmodel.Request{three, two, one, cancelled, overdue, expired} {
		if err := requests.Create(t.Context(), request); err != nil {
			t.Fatal(err)
		}
	}
	if err := requests.Cancel(t.Context(), cancelled.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := requests.Expire(t.Context(), expired.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	server := startAPI(t, fixture.localArgs())
	path := filepath.Join(fixture.root, "database", "secret-requests.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		query string
		want  []string
	}{
		{"", []string{one.ID, two.ID, three.ID}},
		{"?agent_type=builder-agent", []string{one.ID, two.ID}},
		{"?agent_id=" + one.AgentID, []string{one.ID}},
		{"?agent_type=builder-agent&agent_id=" + two.AgentID, []string{two.ID}},
		{"?agent_type=web-agent&agent_id=" + one.AgentID, []string{}},
		{"?agent_type=unknown-agent", []string{}},
		{"?agent_id=builder-agent-ffffffff", []string{}},
	} {
		items := listSecretRequests(t, server, tc.query)
		got := make([]string, len(items))
		for index, item := range items {
			got[index] = item.ID
			if item.Status != "needs_value" || item.OperationID != "" {
				t.Fatal("missing secret request had unexpected state or operation")
			}
		}
		sort.Strings(tc.want)
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("query %q returned request IDs %v, want %v in ID order", tc.query, got, tc.want)
		}
	}
	result := secretRequest(t, server, http.MethodGet, secretRequestsPath, "")
	var raw struct {
		Items []map[string]json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal([]byte(result.body), &raw); err != nil {
		t.Fatal(err)
	}
	for _, item := range raw.Items {
		if _, present := item["operation_id"]; present {
			t.Fatal("absent operation ID must be omitted")
		}
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("pending inventory reads mutated saved request history")
	}
}

func TestPendingSecretRequestsRespectSecretAndDeliveryTimeBounds(t *testing.T) {
	fixture := newFixture(t)
	t.Setenv("SECRET_ENCRYPTION_KEY", secretTestKey)
	requests, values := secretRequestStores(t, fixture)
	cipher, err := crypto.NewSecretCipher(secretTestKey)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	want := map[string]string{"EXPIRED_TOKEN": "needs_value", "FUTURE_TOKEN": "needs_value", "RESERVED_LATER_TOKEN": "awaiting_delivery"}
	for name := range want {
		request := pendingSecretRequest("builder-agent", name, "builder-agent-00000001")
		request.RequestedAt, request.ExpiresAt = now.Add(-2*time.Hour), now.Add(2*time.Hour)
		if err := requests.Create(t.Context(), request); err != nil {
			t.Fatal(err)
		}
		created := now.Add(-3 * time.Hour)
		metadata := secretmodel.Metadata{ID: id.New(), Key: request.Key, Version: 1, CreatedAt: created, UpdatedAt: created}
		if name == "EXPIRED_TOKEN" {
			metadata.ExpiresAt = now.Add(-time.Hour)
		} else if name == "FUTURE_TOKEN" {
			metadata.CreatedAt, metadata.UpdatedAt = now.Add(time.Hour), now.Add(time.Hour)
		}
		encrypted, err := cipher.Encrypt(metadata, []byte(secretTestValue))
		if err != nil {
			t.Fatal(err)
		}
		if err := values.Create(t.Context(), encrypted); err != nil {
			t.Fatal(err)
		}
		if name == "RESERVED_LATER_TOKEN" {
			if err := requests.SaveDelivery(t.Context(), secretmodel.Delivery{
				ID: id.New(), RequestID: request.ID, AgentID: request.AgentID, Secret: encrypted,
				CreatedAt: now.Add(time.Hour), ExpiresAt: request.ExpiresAt,
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
	server := startAPI(t, fixture.localArgs())
	items := listSecretRequests(t, server, "")
	if len(items) != len(want) {
		t.Fatal("pending requests were lost while projecting availability")
	}
	for _, item := range items {
		if item.Status != want[item.Name] {
			t.Errorf("%s status = %q, want %q", item.Name, item.Status, want[item.Name])
		}
	}
}

func TestPendingSecretRequestsRequireAuthenticationAndEnabledSecrets(t *testing.T) {
	fixture := newFixture(t)
	server := startAPI(t, fixture.localArgs())
	assertSecretError(t, server.request(t, http.MethodGet, secretRequestsPath), http.StatusUnauthorized, "authentication_required")
	disabled := secretRequest(t, server, http.MethodGet, secretRequestsPath, "")
	assertSecretError(t, disabled, http.StatusServiceUnavailable, "secrets_unavailable")
	if disabled.header.Get("Cache-Control") != "no-store" {
		t.Fatal("disabled request inventory may be cached")
	}
	for _, name := range []string{"secrets.json", "secret-requests.json"} {
		if _, err := os.Stat(filepath.Join(fixture.root, "database", name)); !os.IsNotExist(err) {
			t.Fatalf("disabled secret feature opened %s", name)
		}
	}
	server.stop(t)
	t.Setenv("SECRET_ENCRYPTION_KEY", secretTestKey)
	server = startAPI(t, fixture.localArgs())
	if items := listSecretRequests(t, server, ""); len(items) != 0 {
		t.Fatal("empty database had pending requests")
	}
	for _, query := range []string{
		"?agent_type=", "?agent_id=", "?agent_type=../builder", "?agent_id=private-invalid-id",
		"?agent_type=builder-agent&agent_type=web-agent", "?agent_id=builder-agent-00000001&agent_id=builder-agent-00000002",
		"?value=private-query-value", "?status=pending", "?agent_type=%zz",
	} {
		result := secretRequest(t, server, http.MethodGet, secretRequestsPath+query, "")
		assertSecretError(t, result, http.StatusBadRequest, "invalid_request")
		if result.header.Get("Cache-Control") != "no-store" {
			t.Fatal("invalid pending inventory query may be cached")
		}
	}
}

func secretRequestStores(t *testing.T, fixture fixture) (secretmodel.RequestStore, secretmodel.SecretStore) {
	t.Helper()
	path := filepath.Join(fixture.root, "database")
	requests, err := fssecrets.NewRequestFsStore(path)
	if err != nil {
		t.Fatal(err)
	}
	values, err := fssecrets.NewSecretFsStore(path)
	if err != nil {
		t.Fatal(err)
	}
	return requests, values
}

func pendingSecretRequest(agentType, name, agentID string) secretmodel.Request {
	now := time.Now().UTC()
	return secretmodel.Request{
		ID: id.New(), Key: secretmodel.Key{Scope: secretmodel.Scope{AgentType: agentType}, Name: name},
		AgentID: agentID, Status: secretmodel.Pending, RequestedAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour),
	}
}

func listSecretRequests(t *testing.T, server *runningAPI, query string) []pendingSecretView {
	t.Helper()
	result := secretRequest(t, server, http.MethodGet, secretRequestsPath+query, "")
	if result.header.Get("Cache-Control") != "no-store" {
		t.Fatal("pending secret request metadata may be cached")
	}
	payload := decodeSecretResponse[struct {
		Items []pendingSecretView `json:"items"`
	}](t, result, http.StatusOK)
	if payload.Items == nil {
		t.Fatal("empty pending request inventory must use an array")
	}
	return payload.Items
}

func reserveSecretDelivery(t *testing.T, requests secretmodel.RequestStore, values secretmodel.SecretStore, request secretmodel.Request) secretmodel.Delivery {
	t.Helper()
	secret, err := values.Get(t.Context(), request.Key)
	if err != nil {
		t.Fatal(err)
	}
	deadline := request.ExpiresAt
	if expiry := secret.Metadata.ExpiresAt; !expiry.IsZero() && expiry.Before(deadline) {
		deadline = expiry
	}
	delivery := secretmodel.Delivery{
		ID: id.New(), RequestID: request.ID, AgentID: request.AgentID, Secret: secret,
		CreatedAt: time.Now().UTC(), ExpiresAt: deadline,
	}
	if err := requests.SaveDelivery(t.Context(), delivery); err != nil {
		t.Fatal(err)
	}
	return delivery
}
