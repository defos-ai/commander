package connections

import (
	"bytes"
	"errors"
	"io"
	"net/http"
)

func (h *Handler) proxy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodPost && r.Method != http.MethodPut && r.Method != http.MethodPatch && r.Method != http.MethodDelete {
		methodNotAllowed(w, "GET, HEAD, POST, PUT, PATCH, DELETE")
		return
	}
	target, err := normalizeBase(r.URL.Query().Get("url"))
	if err != nil {
		fail(w, http.StatusBadRequest, "Invalid API URL.")
		return
	}
	key := target.String()
	upstream, client := h.resolveTarget(target)
	address, public, err := endpoint(upstream, r.URL.Query().Get("path"))
	if err != nil || (public && r.Method != http.MethodGet && r.Method != http.MethodHead) {
		fail(w, http.StatusBadRequest, "Invalid API endpoint.")
		return
	}
	id := sessionID(r)
	var value *credentials
	if !public {
		value = h.sessions.get(id, key)
		if value == nil {
			fail(w, http.StatusUnauthorized, "Sign in to this API.")
			return
		}
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			fail(w, http.StatusRequestEntityTooLarge, "API request is too large.")
		} else {
			fail(w, http.StatusBadRequest, "Could not read API request.")
		}
		return
	}
	request, err := http.NewRequestWithContext(r.Context(), r.Method, address.String(), bytes.NewReader(body))
	if err != nil {
		fail(w, http.StatusBadRequest, "Invalid API request.")
		return
	}
	for _, name := range []string{"Accept", "Content-Type", "Range", "If-Range", "If-None-Match", "If-Modified-Since"} {
		if header := r.Header.Get(name); header != "" {
			request.Header.Set(name, header)
		}
	}
	if value != nil {
		request.SetBasicAuth(value.username, value.password)
	}
	// Both clients from resolveTarget enforce apiDialer checks before connecting;
	// private access is limited to configured mappings and local API discovery.
	response, err := client.Do(request)
	if err != nil {
		if failUnsafeTarget(w, err) {
			return
		}
		fail(w, http.StatusBadGateway, "API request did not complete. Check its outcome before retrying a command.")
		return
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 && response.StatusCode < 400 && response.StatusCode != http.StatusNotModified {
		fail(w, http.StatusBadGateway, "API redirects are not supported.")
		return
	}
	if response.StatusCode == http.StatusUnauthorized && value != nil {
		h.sessions.remove(id, key, value)
	}
	for _, name := range []string{"Content-Type", "Content-Length", "Content-Encoding", "Content-Disposition", "Content-Range", "Accept-Ranges", "ETag", "Last-Modified", "Retry-After"} {
		if header := response.Header.Get(name); header != "" {
			w.Header().Set(name, header)
		}
	}
	w.WriteHeader(response.StatusCode)
	if r.Method != http.MethodHead {
		if _, err := io.Copy(w, response.Body); err != nil {
			panic(http.ErrAbortHandler)
		}
	}
}
