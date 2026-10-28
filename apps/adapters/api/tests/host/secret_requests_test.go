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

	hostfixtures "github.com/defos-ai/commander/apps/adapters/api/tests/fixtures/host"
	"github.com/defos-ai/commander/framework/crypto"
	secretmodel "github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/id"
)

const secretRequestsPath = "/api/v1/secret-requests"

type pendingSecretView struct {
	ID          string    `json:"id"`
	AgentType   string    `json:"agent_type"`
	Name        string    `json:"name"`
	AgentID     string    `json:"agent_id"`
	RequestedAt time.Time `json:"requested_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	Status      string    `json:"status"`
}

func TestPendingSecretRequestsFollowValueDeliveryAndInstallation(t *testing.T) {
	fixture := hostfixtures.New(t)
	t.Setenv("SECRETS_ENCRYPTION_KEY", hostfixtures.SecretTestKey)
	requests, values := hostfixtures.SecretRequestStores(t, fixture)
	request := hostfixtures.PendingSecretRequest("builder-agent", "GITHUB_TOKEN", "builder-agent-00000001")
	if err := requests.Create(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	server := hostfixtures.Start(t, fixture.LocalArgs())
	assertStatus := func(want string) {
		t.Helper()
		items := listSecretRequests(t, server, "")
		if len(items) != 1 {
			t.Fatalf("pending inventory has %d requests, want one", len(items))
		}
		expected := pendingSecretView{
			ID: request.ID, AgentType: request.AgentType, Name: request.Key.Name,
			AgentID:     request.AgentID,
			RequestedAt: request.RequestedAt, ExpiresAt: request.ExpiresAt, Status: want,
		}
		if !reflect.DeepEqual(items[0], expected) {
			t.Fatalf("pending metadata = %+v, want %+v", items[0], expected)
		}
	}
	assertStatus("needs_value")
	createBody := `{"name":"GITHUB_TOKEN","value":"` + hostfixtures.SecretTestValue + `"}`
	created := decodeSecretResponse[secretMetadata](t, secretRequest(t, server, http.MethodPost, hostfixtures.SecretsPath, createBody), http.StatusCreated)
	assertStatus("awaiting_delivery")
	hostfixtures.ReserveSecretDelivery(t, requests, values, request)
	assertStatus("awaiting_installation")
	// A reservation is not an acknowledgement, and reads must not settle it.
	unchanged, err := requests.Get(t.Context(), request.ID)
	if err != nil || unchanged.Request.Status != secretmodel.Pending || unchanged.Installation != nil {
		t.Fatal("reading a reserved delivery claimed installation")
	}
	replaced := decodeSecretResponse[secretMetadata](t, secretRequest(t, server, http.MethodPut, hostfixtures.SecretPath,
		fmt.Sprintf(`{"id":%q,"version":1,"value":%q}`, created.ID, hostfixtures.SecretTestReplacement)), http.StatusOK)
	assertStatus("awaiting_delivery")
	deleted := secretRequest(t, server, http.MethodDelete,
		fmt.Sprintf("%s?id=%s&version=%d", hostfixtures.SecretPath, replaced.ID, replaced.Version), "")
	if deleted.Status != http.StatusNoContent {
		t.Fatal("could not remove test secret")
	}
	assertStatus("needs_value")
	recreated := decodeSecretResponse[secretMetadata](t, secretRequest(t, server, http.MethodPost, hostfixtures.SecretsPath, createBody), http.StatusCreated)
	if recreated.ID == created.ID || recreated.Version != 1 {
		t.Fatal("recreation did not allocate a new identity")
	}
	assertStatus("awaiting_delivery")
	if err := requests.Cancel(t.Context(), request.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	request = hostfixtures.PendingSecretRequest("builder-agent", "GITHUB_TOKEN", request.AgentID)
	if err := requests.Create(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	delivery := hostfixtures.ReserveSecretDelivery(t, requests, values, request)
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
	fixture := hostfixtures.New(t)
	t.Setenv("SECRETS_ENCRYPTION_KEY", hostfixtures.SecretTestKey)
	requests, _ := hostfixtures.SecretRequestStores(t, fixture)
	one := hostfixtures.PendingSecretRequest("builder-agent", "GITHUB_TOKEN", "builder-agent-00000001")
	two := hostfixtures.PendingSecretRequest("builder-agent", "BUILD_TOKEN", "builder-agent-00000002")
	three := hostfixtures.PendingSecretRequest("web-agent", "SERVICE_TOKEN", "web-agent-00000001")
	cancelled := hostfixtures.PendingSecretRequest("builder-agent", "CANCELLED_TOKEN", one.AgentID)
	overdue := hostfixtures.PendingSecretRequest("builder-agent", "OVERDUE_TOKEN", one.AgentID)
	overdue.ExpiresAt = time.Now().UTC().Add(-time.Minute)
	expired := hostfixtures.PendingSecretRequest("builder-agent", "EXPIRED_TOKEN", one.AgentID)
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
	server := hostfixtures.Start(t, fixture.LocalArgs())
	path := filepath.Join(fixture.Root, "database", "secret-requests.json")
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
			if item.Status != "needs_value" {
				t.Fatal("missing secret request had unexpected state")
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
	if err := json.Unmarshal([]byte(result.Body), &raw); err != nil {
		t.Fatal(err)
	}
	for _, item := range raw.Items {
		if _, present := item["operation_id"]; present {
			t.Fatal("agent secret requests must not expose an operation link")
		}
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("pending inventory reads mutated saved request history")
	}
}

func TestPendingSecretRequestsRespectSecretAndDeliveryTimeBounds(t *testing.T) {
	fixture := hostfixtures.New(t)
	t.Setenv("SECRETS_ENCRYPTION_KEY", hostfixtures.SecretTestKey)
	requests, values := hostfixtures.SecretRequestStores(t, fixture)
	cipher, err := crypto.NewSecretCipher(hostfixtures.SecretTestKey)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	want := map[string]string{"EXPIRED_TOKEN": "needs_value", "FUTURE_TOKEN": "needs_value", "RESERVED_LATER_TOKEN": "awaiting_delivery"}
	for name := range want {
		request := hostfixtures.PendingSecretRequest("builder-agent", name, "builder-agent-00000001")
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
		encrypted, err := cipher.Encrypt(metadata, []byte(hostfixtures.SecretTestValue))
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
	server := hostfixtures.Start(t, fixture.LocalArgs())
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
	fixture := hostfixtures.New(t)
	server := hostfixtures.Start(t, fixture.LocalArgs())
	assertSecretError(t, server.Request(t, http.MethodGet, secretRequestsPath), http.StatusUnauthorized, "authentication_required")
	disabled := secretRequest(t, server, http.MethodGet, secretRequestsPath, "")
	assertSecretError(t, disabled, http.StatusServiceUnavailable, "secrets_unavailable")
	if disabled.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("disabled request inventory may be cached")
	}
	for _, name := range []string{"secrets.json", "secret-requests.json"} {
		if _, err := os.Stat(filepath.Join(fixture.Root, "database", name)); !os.IsNotExist(err) {
			t.Fatalf("disabled secret feature opened %s", name)
		}
	}
	server.Stop(t)
	t.Setenv("SECRETS_ENCRYPTION_KEY", hostfixtures.SecretTestKey)
	server = hostfixtures.Start(t, fixture.LocalArgs())
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
		if result.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("invalid pending inventory query may be cached")
		}
	}
}

func listSecretRequests(t *testing.T, server *hostfixtures.API, query string) []pendingSecretView {
	t.Helper()
	result := secretRequest(t, server, http.MethodGet, secretRequestsPath+query, "")
	if result.Header.Get("Cache-Control") != "no-store" {
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
