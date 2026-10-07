package api

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/defos-ai/commander/apps/adapters/api/endpoints"
)

type corsPolicy struct {
	origins map[requestOrigin]struct{}
}

func newCORSPolicy(values []string) (corsPolicy, error) {
	policy := corsPolicy{origins: make(map[requestOrigin]struct{}, len(values))}
	for _, value := range values {
		origin, err := parseOrigin(value)
		if err != nil {
			return corsPolicy{}, errors.New("allowed-origins must contain exact HTTP(S) origins without credentials, paths, queries, fragments or wildcards")
		}
		policy.origins[origin] = struct{}{}
	}
	return policy, nil
}

func parseOrigin(value string) (requestOrigin, error) {
	parsed, err := url.Parse(value)
	if err != nil || strings.ContainsAny(value, "?#") || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.User != nil || parsed.Path != "" || parsed.Opaque != "" {
		return requestOrigin{}, errors.New("invalid origin")
	}
	host, port, err := parseAuthority(parsed.Host)
	if err != nil {
		return requestOrigin{}, err
	}
	origin := requestOrigin{scheme: parsed.Scheme, host: host, port: port}
	origin.port = origin.effectivePort()
	return origin, nil
}

func (p corsPolicy) applyHeaders(w http.ResponseWriter, r *http.Request) bool {
	if len(p.origins) == 0 {
		return false
	}
	w.Header().Add("Vary", "Origin")
	if corsPreflight(r) {
		w.Header().Add("Vary", "Access-Control-Request-Method")
		w.Header().Add("Vary", "Access-Control-Request-Headers")
	}
	values := r.Header.Values("Origin")
	if len(values) != 1 || !validFetchSite(r, true) {
		return false
	}
	origin, err := parseOrigin(values[0])
	if err != nil {
		return false
	}
	if _, allowed := p.origins[origin]; !allowed {
		return false
	}
	w.Header().Set("Access-Control-Allow-Origin", values[0])
	w.Header().Set("Access-Control-Expose-Headers", "Location, Content-Disposition, WWW-Authenticate")
	return true
}

func validFetchSite(r *http.Request, crossOrigin bool) bool {
	values := r.Header.Values("Sec-Fetch-Site")
	if len(values) == 0 {
		return true
	}
	if len(values) != 1 {
		return false
	}
	switch values[0] {
	case "same-origin", "none":
		return true
	case "same-site", "cross-site":
		return crossOrigin
	default:
		return false
	}
}

func corsPreflight(r *http.Request) bool {
	return r.Method == http.MethodOptions && len(r.Header.Values("Origin")) > 0 && len(r.Header.Values("Access-Control-Request-Method")) > 0
}

func writePreflight(w http.ResponseWriter, r *http.Request) {
	methods := r.Header.Values("Access-Control-Request-Method")
	if len(methods) != 1 || !validHTTPToken(methods[0]) {
		endpoints.WriteError(w, http.StatusBadRequest, "invalid_cors_preflight")
		return
	}
	switch methods[0] {
	case http.MethodGet, http.MethodHead, http.MethodPost:
	default:
		endpoints.WriteError(w, http.StatusForbidden, "cors_method_not_allowed")
		return
	}
	for _, value := range r.Header.Values("Access-Control-Request-Headers") {
		for _, name := range strings.Split(value, ",") {
			switch strings.ToLower(strings.TrimSpace(name)) {
			case "authorization", "content-type", "accept":
			default:
				endpoints.WriteError(w, http.StatusForbidden, "cors_headers_not_allowed")
				return
			}
		}
	}
	w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, POST")
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Accept")
	w.Header().Set("Access-Control-Max-Age", "600")
	w.WriteHeader(http.StatusNoContent)
}

func validHTTPToken(value string) bool {
	return value != "" && !strings.ContainsFunc(value, func(c rune) bool {
		return !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", c))
	})
}
