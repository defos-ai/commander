package endpoints

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"commander/framework/domain/actions"
	"commander/framework/domain/agents"
	"commander/framework/domain/missions"
)

type catalogList[T any] struct {
	Items []T `json:"items"`
}

func listResponse[T any](items []T) catalogList[T] {
	if items == nil {
		items = []T{}
	}
	return catalogList[T]{Items: items}
}

func writeQueryResult(w http.ResponseWriter, value any, err error) {
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, value)
	case errors.Is(err, missions.ErrUnknownMission):
		WriteError(w, http.StatusNotFound, "mission_not_found")
	case errors.Is(err, actions.ErrUnknownAction):
		WriteError(w, http.StatusNotFound, "action_not_found")
	case errors.Is(err, agents.ErrUnknownAgent):
		WriteError(w, http.StatusNotFound, "agent_not_found")
	case errors.Is(err, context.DeadlineExceeded):
		WriteError(w, http.StatusGatewayTimeout, "request_timeout")
	default:
		WriteError(w, http.StatusInternalServerError, "internal_error")
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
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
