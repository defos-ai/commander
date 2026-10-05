package endpoints

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"commander/framework/domain/actions"
	"commander/framework/domain/agents"
	"commander/framework/domain/missions"
	"commander/framework/domain/operations"
	"commander/framework/domain/reports"
)

type List[T any] struct {
	Items []T `json:"items"`
}

func ListResponse[T any](items []T) List[T] {
	if items == nil {
		items = []T{}
	}
	return List[T]{Items: items}
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
	case errors.Is(err, operations.ErrUnknownOperation):
		WriteError(w, http.StatusNotFound, "operation_not_found")
	case errors.Is(err, operations.ErrUnknownTask):
		WriteError(w, http.StatusNotFound, "task_not_found")
	case errors.Is(err, reports.ErrUnknownReport):
		WriteError(w, http.StatusNotFound, "report_not_found")
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
