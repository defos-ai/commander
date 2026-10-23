package main

import (
	"net/http"
	"sync"

	"github.com/defos-ai/commander/framework/id"
)

// These fixture-only gates let tests pause agent readiness without controlling
// Docker or altering saved registrations. The host releases them over loopback.
func registerGates(mux *http.ServeMux) {
	var mu sync.Mutex
	gates := make(map[string]chan struct{})
	mux.HandleFunc("/gates/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !id.Valid(r.PathValue("id")) {
			http.Error(w, "invalid gate", http.StatusBadRequest)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		mu.Lock()
		gate := gates[r.PathValue("id")]
		if gate == nil {
			gate = make(chan struct{})
			gates[r.PathValue("id")] = gate
		}
		if r.Method == http.MethodPost {
			select {
			case <-gate:
			default:
				close(gate)
			}
		}
		mu.Unlock()
		select {
		case <-gate:
			w.WriteHeader(http.StatusNoContent)
		case <-r.Context().Done():
		}
	})
}
