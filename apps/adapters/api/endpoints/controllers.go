package endpoints

import (
	"cmp"
	"net/http"
	"slices"
	"time"

	"github.com/defos-ai/commander/apps/adapters/api/mappings"
	"github.com/defos-ai/commander/framework/domain/controllers"
)

func RegisterControllers(mux *http.ServeMux, store controllers.ControllerStore) {
	mux.HandleFunc("GET /api/v1/controllers", RequestHandler(func(w http.ResponseWriter, r *http.Request) {
		items, err := store.List(r.Context())
		now := time.Now().UTC()
		for i := range items {
			items[i] = items[i].Snapshot(now)
		}
		slices.SortFunc(items, func(a, b controllers.Controller) int { return cmp.Compare(a.ID, b.ID) })
		WriteQueryResult(w, ListResponse(mappings.ToControllerResponses(items)), err)
	}))
	mux.HandleFunc("GET /api/v1/controllers/{controller_id}", RequestHandler(func(w http.ResponseWriter, r *http.Request) {
		item, err := store.Get(r.Context(), r.PathValue("controller_id"))
		WriteQueryResult(w, mappings.ToControllerResponse(item.Snapshot(time.Now().UTC())), err)
	}))
}
