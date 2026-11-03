package sessionworker

import (
	"net/http"
	"sync/atomic"
)

type NetworkPeer struct{ paused atomic.Bool }

func (p *NetworkPeer) Pause()  { p.paused.Store(true) }
func (p *NetworkPeer) Resume() { p.paused.Store(false) }

func (p *NetworkPeer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/network" && p.paused.Load() {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
