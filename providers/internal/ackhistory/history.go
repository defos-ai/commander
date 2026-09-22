// Package ackhistory tracks a bounded window of acknowledged message IDs.
package ackhistory

// A bounded history makes ordinary ACK retries idempotent without retaining one
// entry for every message ever processed by a long-running subscription.
const limit = 4096

// History retains recent acknowledgements. Its zero value is ready for use.
// Callers must serialize access.
type History struct {
	ids   map[string]struct{}
	order []string
	next  int
}

// Contains reports whether id is in the recent acknowledgement window.
func (h *History) Contains(id string) bool {
	_, ok := h.ids[id]
	return ok
}

// Remember records id, evicting the oldest entry when the window is full.
func (h *History) Remember(id string) {
	if h.Contains(id) {
		return
	}
	if h.ids == nil {
		h.ids = make(map[string]struct{})
	}
	if len(h.order) < limit {
		h.order = append(h.order, id)
	} else {
		delete(h.ids, h.order[h.next])
		h.order[h.next] = id
		h.next = (h.next + 1) % limit
	}
	h.ids[id] = struct{}{}
}
