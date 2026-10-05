package endpoints

import (
	"cmp"
	"net/http"
	"slices"

	"commander/framework/domain/actions"
)

func RegisterActions(mux *http.ServeMux, store actions.ActionStore) {
	mux.HandleFunc("GET /api/v1/actions", catalogQuery(func(w http.ResponseWriter, r *http.Request) {
		items, err := store.List(r.Context())
		slices.SortFunc(items, func(a, b actions.Action) int { return cmp.Compare(a.Name, b.Name) })
		writeQueryResult(w, listResponse(items), err)
	}))
	mux.HandleFunc("GET /api/v1/actions/{action_name}", catalogQuery(func(w http.ResponseWriter, r *http.Request) {
		item, err := store.Get(r.Context(), r.PathValue("action_name"))
		writeQueryResult(w, item, err)
	}))
}
