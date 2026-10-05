package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"commander/adapters/api/endpoints"
	operationendpoints "commander/adapters/api/endpoints/operations"
	controllercommands "commander/framework/comms/clients/controller/commands"
	"commander/framework/domain/actions"
	"commander/framework/domain/agents"
	"commander/framework/domain/artifacts"
	"commander/framework/domain/missions"
	"commander/framework/domain/operations"
	"commander/framework/domain/reports"
)

type Config struct {
	Security          SecurityConfig
	SwaggerEnabled    bool
	Operations        controllercommands.Operations
	MissionStore      missions.MissionStore
	OperationStore    operations.OperationStore
	AgentStore        agents.AgentStore
	ActionStore       actions.ActionStore
	ReportStore       reports.ReportStore
	ArtifactStore     artifacts.ArtifactStore
	ArtifactFileStore artifacts.ArtifactFileStore
}

func NewHandler(config Config) (http.Handler, error) {
	security, err := newSecurity(config.Security)
	if err != nil {
		return nil, err
	}
	if config.Operations == nil || config.MissionStore == nil || config.OperationStore == nil ||
		config.AgentStore == nil || config.ActionStore == nil || config.ReportStore == nil ||
		config.ArtifactStore == nil || config.ArtifactFileStore == nil {
		return nil, errors.New("API requires controller commands and mission, operation, agent, action, report, artifact metadata and artifact file stores")
	}
	mux := http.NewServeMux()
	endpoints.RegisterMissions(mux, config.MissionStore)
	endpoints.RegisterActions(mux, config.ActionStore)
	endpoints.RegisterAgents(mux, config.AgentStore)
	endpoints.RegisterReports(mux, config.ReportStore)
	operationendpoints.Register(mux, config.OperationStore, config.ReportStore, config.Operations)
	if config.SwaggerEnabled {
		if err := registerDocumentation(mux); err != nil {
			return nil, err
		}
	}
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeHealth(w, http.StatusOK, "ok")
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		// The bus has no health-check contract. This probe checks the metadata
		// database; it does not assert controller availability or file-store health.
		if _, err := config.MissionStore.List(ctx); err != nil {
			writeHealth(w, http.StatusServiceUnavailable, "unavailable")
			return
		}
		writeHealth(w, http.StatusOK, "ok")
	})
	return security.protect(mux), nil
}

func writeHealth(w http.ResponseWriter, status int, value string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, "{\"status\":\""+value+"\"}\n")
}
