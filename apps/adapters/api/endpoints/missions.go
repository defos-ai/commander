package endpoints

import (
	"cmp"
	"net/http"
	"slices"

	"commander/framework/domain/missions"
)

func RegisterMissions(mux *http.ServeMux, store missions.MissionStore) {
	mux.HandleFunc("GET /api/v1/missions", RequestHandler(func(w http.ResponseWriter, r *http.Request) {
		items, err := store.List(r.Context())
		slices.SortFunc(items, func(a, b missions.Mission) int { return cmp.Compare(a.Name, b.Name) })
		WriteQueryResult(w, ListResponse(items), err)
	}))
	mux.HandleFunc("GET /api/v1/missions/{mission_name}", RequestHandler(func(w http.ResponseWriter, r *http.Request) {
		item, err := store.Get(r.Context(), r.PathValue("mission_name"))
		WriteQueryResult(w, item, err)
	}))
}
