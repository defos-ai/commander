package endpoints

import (
	"errors"
	"net/http"

	frameworktesting "github.com/defos-ai/commander/framework/testing"
)

func RegisterTestCleanup(mux *http.ServeMux, cleaner frameworktesting.TestCleaner) {
	mux.HandleFunc("POST /api/v1/testing/cleanup", RequestHandler(func(w http.ResponseWriter, r *http.Request) {
		var request struct{}
		if !ReadJSON(w, r, &request, true) {
			return
		}
		switch err := frameworktesting.CleanContext(r.Context(), cleaner); {
		case errors.Is(err, frameworktesting.ErrBusy):
			WriteError(w, http.StatusConflict, "test_state_busy")
		case err != nil:
			WriteQueryResult(w, nil, err)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
}
