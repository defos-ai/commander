package host

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/framework/agent/packages"
	"github.com/defos-ai/commander/framework/domain/agents"
)

const CatalogPackage = "github.com/defos-ai/custom-agent/modules/worker@v1.2.3"

func CatalogEntry(value string) agents.CatalogEntry {
	created := time.Date(2026, 10, 1, 9, 30, 0, 123456789, time.UTC)
	return agents.CatalogEntry{Name: "custom-agent", Description: "A custom worker", Package: value,
		Visibility: packages.Private, Version: 1, CreatedAt: created, UpdatedAt: created}
}

func CatalogCreateBody(value string) string {
	data, _ := json.Marshal(models.CreateCatalogAgentRequest{
		Name: "custom-agent", Description: "A custom worker", Package: value, Visibility: "private",
	})
	return string(data)
}

func CatalogRequest(ctx context.Context, handler http.Handler, method, path, body string, authenticate bool, contentType ...string) Response {
	request := httptest.NewRequestWithContext(ctx, method, "http://localhost"+path, strings.NewReader(body))
	if authenticate {
		request.SetBasicAuth("log-user", "log-password")
	}
	mediaType := "application/json"
	if len(contentType) != 0 {
		mediaType = contentType[0]
	}
	request.Header.Set("Content-Type", mediaType)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return Response{Status: recorder.Code, Header: recorder.Header(), Body: recorder.Body.String()}
}
