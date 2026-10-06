package endpoints

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"commander/adapters/api/models"
	"commander/framework/domain/actions"
	"commander/framework/domain/agents"
	"commander/framework/domain/artifacts"
	"commander/framework/domain/controllers"
	"commander/framework/domain/missions"
	"commander/framework/domain/operations"
	"commander/framework/domain/reports"
)

func ListResponse[T any](items []T) models.ListResponse[T] {
	if items == nil {
		items = []T{}
	}
	return models.ListResponse[T]{Items: items}
}

func WriteQueryResult(w http.ResponseWriter, value any, err error) {
	switch {
	case err == nil:
		WriteJSON(w, http.StatusOK, value)
	case errors.Is(err, missions.ErrUnknownMission):
		WriteError(w, http.StatusNotFound, "mission_not_found")
	case errors.Is(err, actions.ErrUnknownAction):
		WriteError(w, http.StatusNotFound, "action_not_found")
	case errors.Is(err, agents.ErrUnknownAgent):
		WriteError(w, http.StatusNotFound, "agent_not_found")
	case errors.Is(err, controllers.ErrUnknownController):
		WriteError(w, http.StatusNotFound, "unknown_controller")
	case errors.Is(err, operations.ErrUnknownOperation):
		WriteError(w, http.StatusNotFound, "operation_not_found")
	case errors.Is(err, operations.ErrUnknownTask):
		WriteError(w, http.StatusNotFound, "task_not_found")
	case errors.Is(err, reports.ErrUnknownReport):
		WriteError(w, http.StatusNotFound, "report_not_found")
	case errors.Is(err, artifacts.ErrUnknownArtifact):
		WriteError(w, http.StatusNotFound, "artifact_not_found")
	case errors.Is(err, context.DeadlineExceeded):
		WriteError(w, http.StatusGatewayTimeout, "request_timeout")
	default:
		WriteError(w, http.StatusInternalServerError, "internal_error")
	}
}

func WriteJSON(w http.ResponseWriter, status int, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "internal_error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(append(data, '\n'))
}

func WriteError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Error string `json:"error"`
	}{Error: code})
}
