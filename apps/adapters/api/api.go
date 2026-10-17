package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/defos-ai/commander/apps/adapters/api/endpoints"
	operationendpoints "github.com/defos-ai/commander/apps/adapters/api/endpoints/operations"
	controllercommands "github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/artifacts"
	"github.com/defos-ai/commander/framework/domain/controllers"
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/domain/reports"
	"github.com/defos-ai/commander/framework/domain/secrets"
	secretservice "github.com/defos-ai/commander/framework/secrets"
	frameworktesting "github.com/defos-ai/commander/framework/testing"
)

type Config struct {
	Security          SecurityConfig
	Deployment        DeploymentConfig
	SwaggerEnabled    bool
	Operations        controllercommands.Operations
	MissionStore      missions.MissionStore
	OperationStore    operations.OperationStore
	AgentStore        agents.AgentStore
	ControllerStore   controllers.ControllerStore
	ActionStore       actions.ActionStore
	ReportStore       reports.ReportStore
	ArtifactStore     artifacts.ArtifactStore
	ArtifactFileStore artifacts.ArtifactFileStore
	// SecretService is optional; secret routes return 503 when it is absent.
	SecretService *secretservice.Service
	// SecretRequestStore enables pending-request metadata alongside SecretService.
	SecretRequestStore secrets.RequestStore
	TestCleaner        frameworktesting.TestCleaner
}

func NewHandler(config Config) (http.Handler, error) {
	security, err := newSecurity(config.Security)
	if err != nil {
		return nil, err
	}
	if err := config.Deployment.Validate(); err != nil {
		return nil, err
	}
	if config.Operations == nil || config.MissionStore == nil || config.OperationStore == nil ||
		config.AgentStore == nil || config.ControllerStore == nil || config.ActionStore == nil || config.ReportStore == nil ||
		config.ArtifactStore == nil || config.ArtifactFileStore == nil {
		return nil, errors.New("API requires controller commands and mission, operation, agent, controller, action, report, artifact metadata and artifact file stores")
	}
	mux := http.NewServeMux()
	about := config.Deployment.response()
	mux.HandleFunc("GET /aboutz", func(w http.ResponseWriter, _ *http.Request) {
		endpoints.WriteJSON(w, http.StatusOK, about)
	})
	endpoints.RegisterMissions(mux, config.MissionStore)
	endpoints.RegisterActions(mux, config.ActionStore)
	endpoints.RegisterAgents(mux, config.AgentStore)
	endpoints.RegisterControllers(mux, config.ControllerStore)
	endpoints.RegisterReports(mux, config.ReportStore)
	endpoints.RegisterArtifacts(mux, config.ArtifactStore, config.ArtifactFileStore)
	operationendpoints.Register(mux, config.OperationStore, config.ReportStore, config.Operations)
	endpoints.RegisterSecrets(mux, config.SecretService)
	endpoints.RegisterSecretRequests(mux, config.SecretService, config.SecretRequestStore)
	if os.Getenv("ENV") == "tests" && config.TestCleaner != nil {
		endpoints.RegisterTestCleanup(mux, config.TestCleaner)
	}
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
