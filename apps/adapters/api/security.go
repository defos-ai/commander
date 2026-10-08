package api

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"

	"github.com/defos-ai/commander/apps/adapters/api/endpoints"
	"github.com/defos-ai/commander/framework/host"
)

type SecurityConfig struct {
	Username       string
	Password       string
	AllowedHosts   []string
	AllowedOrigins []string
	TrustedProxies []string
	ProxyHeaders   string
}

func (c SecurityConfig) Validate() error {
	_, err := newSecurity(c)
	return err
}

type security struct {
	username [sha256.Size]byte
	password [sha256.Size]byte
	hosts    map[string]struct{}
	proxy    proxyPolicy
	cors     corsPolicy
}

func newSecurity(config SecurityConfig) (*security, error) {
	if err := (host.Credentials{Username: config.Username, Password: config.Password}).Validate(); err != nil {
		return nil, fmt.Errorf("API_USERNAME/API_PASSWORD: %w", err)
	}
	hosts := make(map[string]struct{}, len(config.AllowedHosts))
	for _, value := range config.AllowedHosts {
		host, err := normalizeHostname(value)
		if err != nil {
			return nil, errors.New("allowed-hosts must contain exact hostnames or IPs without ports, URLs or wildcards")
		}
		hosts[host] = struct{}{}
	}
	if len(hosts) == 0 {
		return nil, errors.New("allowed-hosts must not be empty")
	}
	proxy, err := newProxyPolicy(config.TrustedProxies, config.ProxyHeaders)
	if err != nil {
		return nil, err
	}
	cors, err := newCORSPolicy(config.AllowedOrigins)
	if err != nil {
		return nil, err
	}
	return &security{
		username: sha256.Sum256([]byte(config.Username)),
		password: sha256.Sum256([]byte(config.Password)),
		hosts:    hosts, proxy: proxy, cors: cors,
	}, nil
}

func (s *security) protect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		securityHeaders(w)
		corsAllowed := s.cors.applyHeaders(w, r)
		if publicHealth(r) {
			if r.TLS != nil {
				w.Header().Set("Strict-Transport-Security", "max-age=31536000")
			}
			next.ServeHTTP(w, r)
			return
		}
		origin, err := s.proxy.origin(r)
		if err != nil {
			endpoints.WriteError(w, http.StatusBadRequest, "invalid_request_origin")
			return
		}
		if origin.scheme == "https" {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}
		if _, allowed := s.hosts[origin.host]; !allowed {
			endpoints.WriteError(w, http.StatusForbidden, "host_not_allowed")
			return
		}
		if !corsAllowed && !sameOrigin(r, origin) {
			endpoints.WriteError(w, http.StatusForbidden, "cross_origin_request")
			return
		}
		if corsAllowed && corsPreflight(r) {
			writePreflight(w, r)
			return
		}
		if !s.authenticated(r) {
			w.Header().Set("WWW-Authenticate", `Basic realm="Commander", charset="UTF-8"`)
			endpoints.WriteError(w, http.StatusUnauthorized, "authentication_required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *security) authenticated(r *http.Request) bool {
	if len(r.Header.Values("Authorization")) != 1 {
		return false
	}
	username, password, ok := r.BasicAuth()
	userHash, passwordHash := sha256.Sum256([]byte(username)), sha256.Sum256([]byte(password))
	userMatch := subtle.ConstantTimeCompare(userHash[:], s.username[:])
	passwordMatch := subtle.ConstantTimeCompare(passwordHash[:], s.password[:])
	return ok && userMatch&passwordMatch == 1
}

func sameOrigin(r *http.Request, expected requestOrigin) bool {
	if !validFetchSite(r, false) {
		return false
	}
	values := r.Header.Values("Origin")
	if len(values) == 0 {
		return true
	}
	if len(values) != 1 {
		return false
	}
	origin, err := parseOrigin(values[0])
	return err == nil && expected.same(origin)
}

func publicHealth(r *http.Request) bool {
	return (r.Method == http.MethodGet || r.Method == http.MethodHead) && (r.URL.Path == "/healthz" || r.URL.Path == "/readyz")
}

func securityHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
	w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
}
