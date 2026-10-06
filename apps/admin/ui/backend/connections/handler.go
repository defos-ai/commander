package connections

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

const cookieName = "commander_ui_session"

type Config struct {
	PublicOrigin string
	APIUpstreams string
	Now          func() time.Time
}

type Handler struct {
	publicOrigin string
	apiUpstreams map[string]string
	sessions     *sessions
	client       *http.Client
	now          func() time.Time
}

func NewHandler(config Config) (*Handler, error) {
	h := &Handler{now: config.Now}
	if h.now == nil {
		h.now = time.Now
	}
	if config.PublicOrigin != "" {
		var err error
		h.publicOrigin, err = normalizeOrigin(config.PublicOrigin)
		if err != nil {
			return nil, err
		}
	}
	upstreams, err := parseAPIUpstreams(config.APIUpstreams)
	if err != nil {
		return nil, fmt.Errorf("api-upstreams: %w", err)
	}
	h.apiUpstreams = upstreams
	h.client = &http.Client{
		Timeout:       30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{
			DialContext:         (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 25 * time.Second,
			IdleConnTimeout: 90 * time.Second, MaxIdleConns: 32, MaxIdleConnsPerHost: 8, MaxConnsPerHost: 64,
		},
	}
	h.sessions = newSessions(h.now)
	return h, nil
}

func (h *Handler) Close() {
	h.sessions.close()
	h.client.CloseIdleConnections()
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
	if !h.sameOrigin(r) {
		fail(w, http.StatusForbidden, "Request must come from this UI.")
		return
	}
	switch r.URL.Path {
	case "/ui/connections":
		switch r.Method {
		case http.MethodGet:
			h.listConnections(w, r)
		case http.MethodPost:
			h.connect(w, r)
		case http.MethodDelete:
			h.disconnect(w, r)
		default:
			methodNotAllowed(w, "GET, POST, DELETE")
		}
	case "/ui/proxy":
		h.proxy(w, r)
	default:
		fail(w, http.StatusNotFound, "UI endpoint not found.")
	}
}

func (h *Handler) sameOrigin(r *http.Request) bool {
	if r.Header.Get("X-Commander-UI") != "1" {
		return false
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		return false
	}
	expected := h.publicOrigin
	if expected == "" {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		var err error
		expected, err = normalizeOrigin(scheme + "://" + r.Host)
		if err != nil {
			return false
		}
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return r.Method == http.MethodGet || r.Method == http.MethodHead
	}
	normalized, err := normalizeOrigin(origin)
	return err == nil && normalized == expected
}

func (h *Handler) listConnections(w http.ResponseWriter, r *http.Request) {
	id := sessionID(r)
	items, _ := h.sessions.list(id)
	writeJSON(w, struct {
		Items []Connection `json:"items"`
	}{items})
}

func (h *Handler) connect(w http.ResponseWriter, r *http.Request) {
	var input struct {
		URL      string `json:"url"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := readJSON(w, r, &input); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			fail(w, http.StatusRequestEntityTooLarge, "Connection details are too large.")
		} else {
			fail(w, http.StatusBadRequest, "Invalid connection details.")
		}
		return
	}
	target, err := normalizeBase(input.URL)
	if err != nil {
		fail(w, http.StatusBadRequest, "Invalid API URL.")
		return
	}
	key := target.String()
	if input.Username == "" || input.Password == "" || strings.ContainsAny(input.Username, ":\r\n\x00") {
		fail(w, http.StatusBadRequest, "Username and password are required.")
		return
	}
	value := &credentials{username: input.Username, password: input.Password}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	address, _, _ := endpoint(h.resolveTarget(target), "/api/v1/missions")
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address.String(), nil)
	if err != nil {
		fail(w, http.StatusBadGateway, "Could not connect to this API.")
		return
	}
	request.SetBasicAuth(value.username, value.password)
	request.Header.Set("Accept", "application/json")
	response, err := h.client.Do(request)
	if err != nil {
		fail(w, http.StatusBadGateway, "Could not connect to this API.")
		return
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		fail(w, http.StatusUnauthorized, "API credentials were not accepted.")
		return
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	var missions struct {
		Items []json.RawMessage `json:"items"`
	}
	if err != nil || response.StatusCode != http.StatusOK || json.Unmarshal(body, &missions) != nil || missions.Items == nil {
		fail(w, http.StatusBadGateway, "API authentication could not be verified.")
		return
	}
	id, expires, err := h.sessions.save(sessionID(r), key, value)
	if err != nil {
		fail(w, http.StatusInternalServerError, "Could not create a browser session.")
		return
	}
	h.setCookie(w, r, id, expires)
	writeJSON(w, Connection{URL: key, Username: value.username})
}

func (h *Handler) disconnect(w http.ResponseWriter, r *http.Request) {
	target, err := normalizeBase(r.URL.Query().Get("url"))
	if err != nil {
		fail(w, http.StatusBadRequest, "Invalid API URL.")
		return
	}
	if h.sessions.remove(sessionID(r), target.String(), nil) {
		h.clearCookie(w, r)
	}
	w.WriteHeader(http.StatusNoContent)
}

func sessionID(r *http.Request) string {
	cookie, err := r.Cookie(cookieName)
	if err != nil || len(cookie.Value) != 43 {
		return ""
	}
	return cookie.Value
}

func (h *Handler) setCookie(w http.ResponseWriter, r *http.Request, id string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: id, Path: "/ui/", HttpOnly: true, SameSite: http.SameSiteStrictMode,
		Secure:  r.TLS != nil || strings.HasPrefix(h.publicOrigin, "https://"),
		Expires: expires, MaxAge: int(expires.Sub(h.now()).Seconds()),
	})
}

func (h *Handler) clearCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Path: "/ui/", HttpOnly: true, SameSite: http.SameSiteStrictMode,
		Secure:  r.TLS != nil || strings.HasPrefix(h.publicOrigin, "https://"),
		Expires: time.Unix(1, 0), MaxAge: -1,
	})
}

func readJSON(w http.ResponseWriter, r *http.Request, value any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("expected one JSON value")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func fail(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func methodNotAllowed(w http.ResponseWriter, methods string) {
	w.Header().Set("Allow", methods)
	fail(w, http.StatusMethodNotAllowed, "Method not allowed.")
}
