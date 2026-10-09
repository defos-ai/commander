package cli

import (
	"encoding/json"
	"net/http"
)

func aboutHandler(runtime, application string) http.Handler {
	metadata := struct {
		Runtime     string `json:"runtime"`
		Application string `json:"application"`
	}{Runtime: runtime, Application: application}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodHead {
			json.NewEncoder(w).Encode(metadata)
		}
	})
}
