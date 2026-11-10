package cli

import (
	"encoding/json"
	"net/http"
)

func aboutHandler(runtime, application, instanceID string) http.Handler {
	metadata := struct {
		Runtime     string `json:"runtime"`
		Application string `json:"application"`
		InstanceID  string `json:"instance_id,omitempty"`
	}{Runtime: runtime, Application: application, InstanceID: instanceID}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodHead {
			json.NewEncoder(w).Encode(metadata)
		}
	})
}
