package api_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"reflect"
	"testing"

	"commander/framework/domain/actions"
	fsactions "commander/providers/fs/actions"
)

func TestActionsCatalogPreservesPermissionsAndSchemas(t *testing.T) {
	fixture := newFixture(t)
	store, err := fsactions.NewActionFsStore(filepath.Join(fixture.root, "database"))
	if err != nil {
		t.Fatal(err)
	}
	action := actions.Action{
		Name: "discover-pages", Description: "Discover same-hostname pages", Status: actions.Unknown,
		RequiredPermissions: []string{"network-access"},
		InputSchema:         json.RawMessage(`{"type":"object","properties":{"url":{"type":"string"}},"required":["url"]}`),
		OutputSchema:        json.RawMessage(`{"type":"object","properties":{"pages":{"type":"array"}}}`),
	}
	if err := store.Upsert(t.Context(), action); err != nil {
		t.Fatal(err)
	}
	second := action
	second.Name = "capture/page"
	if err := store.Upsert(t.Context(), second); err != nil {
		t.Fatal(err)
	}
	server := startAPI(t, fixture.localArgs())

	detail := decodeResponse[actions.Action](t, server.request(t, http.MethodGet, "/api/v1/actions/discover-pages", authenticate))
	list := decodeResponse[struct {
		Items []actions.Action `json:"items"`
	}](t, server.request(t, http.MethodGet, "/api/v1/actions", authenticate))

	if !reflect.DeepEqual(detail, action) {
		t.Fatalf("action detail lost metadata: got %+v, want %+v", detail, action)
	}
	if len(list.Items) != 2 || list.Items[0].Name != "capture/page" || list.Items[1].Name != "discover-pages" {
		t.Fatalf("unexpected action catalog order: %+v", list.Items)
	}
	escaped := decodeResponse[actions.Action](t, server.request(t, http.MethodGet, "/api/v1/actions/"+url.PathEscape(second.Name), authenticate))
	if !reflect.DeepEqual(escaped, second) {
		t.Fatalf("escaped action name did not resolve: got %+v, want %+v", escaped, second)
	}
	assertAPIError(t, server.request(t, http.MethodGet, "/api/v1/actions/unknown", authenticate), http.StatusNotFound, "action_not_found")
	assertAPIError(t, server.request(t, http.MethodGet, "/api/v1/actions"), http.StatusUnauthorized, "authentication_required")
}
