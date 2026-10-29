package endpoints

import (
	"cmp"
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"

	"github.com/defos-ai/commander/apps/adapters/api/mappings"
	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/framework/domain/missions"
)

func RegisterMissions(mux *http.ServeMux, store missions.MissionStore) {
	mux.HandleFunc("POST /api/v1/missions", RequestHandler(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			WriteError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		var request models.CreateMissionRequest
		if !ReadJSON(w, r, &request, false) {
			return
		}
		mission := mappings.ToMission(request)
		if err := mission.Validate(); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		if err := store.Create(r.Context(), mission); err != nil {
			switch {
			case errors.Is(err, missions.ErrMissionExists):
				WriteError(w, http.StatusConflict, "mission_exists")
			case errors.Is(err, context.Canceled):
				WriteError(w, http.StatusServiceUnavailable, "request_canceled")
			default:
				WriteQueryResult(w, nil, err)
			}
			return
		}
		w.Header().Set("Location", "/api/v1/missions/"+url.PathEscape(mission.Name))
		WriteJSON(w, http.StatusCreated, mappings.ToMissionResponse(mission))
	}))
	mux.HandleFunc("GET /api/v1/missions", RequestHandler(func(w http.ResponseWriter, r *http.Request) {
		items, err := store.List(r.Context())
		slices.SortFunc(items, func(a, b missions.Mission) int { return cmp.Compare(a.Name, b.Name) })
		WriteQueryResult(w, ListResponse(mappings.ToMissionResponses(items)), err)
	}))
	mux.HandleFunc("GET /api/v1/missions/{mission_name}", RequestHandler(func(w http.ResponseWriter, r *http.Request) {
		item, err := store.Get(r.Context(), r.PathValue("mission_name"))
		WriteQueryResult(w, mappings.ToMissionResponse(item), err)
	}))
	mux.HandleFunc("PUT /api/v1/missions/{mission_name}", RequestHandler(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			WriteError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		var request models.CreateMissionRequest
		if !ReadJSON(w, r, &request, false) {
			return
		}
		mission := mappings.ToMission(request)
		if mission.Name != r.PathValue("mission_name") || mission.Validate() != nil {
			WriteError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		_, err := store.Get(r.Context(), mission.Name)
		if err == nil {
			err = store.Upsert(r.Context(), mission)
		}
		if errors.Is(err, context.Canceled) {
			WriteError(w, http.StatusServiceUnavailable, "request_canceled")
			return
		}
		WriteQueryResult(w, mappings.ToMissionResponse(mission), err)
	}))
}
