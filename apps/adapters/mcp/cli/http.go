package cli

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/defos-ai/commander/framework/host"
	"github.com/modelcontextprotocol/go-sdk/auth"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func newMCPHandler(server *sdkmcp.Server, logger *slog.Logger, allowedHosts []string, credentials host.BearerCredentials) http.Handler {
	handler := sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server { return server }, &sdkmcp.StreamableHTTPOptions{
		Stateless: true, JSONResponse: true, Logger: logger,
		PropagateRequestCancellation: true,
		// Our explicit Host allowlist protects every interface, including Docker
		// forwarding, while allowing a configured public host through loopback proxies.
		DisableLocalhostProtection: true,
	})
	return protectHost(allowedHosts, protectOrigin(protectBearer(credentials, handler)))
}

func protectBearer(credentials host.BearerCredentials, next http.Handler) http.Handler {
	expected := sha256.Sum256([]byte(credentials.Token))
	verify := func(_ context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
		actual := sha256.Sum256([]byte(token))
		if subtle.ConstantTimeCompare(actual[:], expected[:]) != 1 {
			return nil, auth.ErrInvalidToken
		}
		return &auth.TokenInfo{UserID: "commander"}, nil
	}
	authenticated := auth.RequireBearerToken(verify, &auth.RequireBearerTokenOptions{AllowMissingExpiration: true})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Del("WWW-Authenticate")
		next.ServeHTTP(w, r)
	}))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		values := r.Header.Values("Authorization")
		if len(values) != 1 {
			http.Error(w, "one bearer token is required", http.StatusUnauthorized)
			return
		}
		scheme, token, _ := strings.Cut(values[0], " ")
		if !strings.EqualFold(scheme, "Bearer") || (host.BearerCredentials{Token: strings.TrimLeft(token, " ")}).Validate() != nil {
			http.Error(w, "a valid bearer token is required", http.StatusUnauthorized)
			return
		}
		authenticated.ServeHTTP(w, r)
	})
}

func normalizeHost(host string) string {
	if ip := net.ParseIP(host); ip != nil {
		return ip.String()
	}
	return strings.ToLower(strings.TrimSuffix(host, "."))
}

func validHostname(host string) bool {
	if net.ParseIP(host) != nil {
		return true
	}
	if host == "" || len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, char := range label {
			if !(char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '-') {
				return false
			}
		}
	}
	return true
}

// Host validation applies regardless of the local interface: Docker forwards
// published loopback ports to a non-loopback address inside the container.
func protectHost(allowed []string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if parsed, _, err := net.SplitHostPort(host); err == nil {
			host = parsed
		} else {
			host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
		}
		host = normalizeHost(host)
		for _, accepted := range allowed {
			if host == accepted {
				next.ServeHTTP(w, r)
				return
			}
		}
		http.Error(w, "HTTP host is not allowed", http.StatusForbidden)
	})
}

// Native MCP clients usually send no Origin. Browser requests must have the
// same origin host, including GET: it can establish a stream in MCP transports.
// The host allowlist separately prevents DNS rebinding on every interface.
func protectOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			http.Error(w, "cross-origin request rejected", http.StatusForbidden)
			return
		}
		if values := r.Header.Values("Origin"); len(values) > 0 {
			origin, err := url.Parse(values[0])
			if len(values) != 1 || err != nil || (origin.Scheme != "http" && origin.Scheme != "https") ||
				origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" ||
				!strings.EqualFold(origin.Host, r.Host) {
				http.Error(w, "cross-origin request rejected", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

type loggedResponse struct {
	http.ResponseWriter
	status int
}

func (w *loggedResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *loggedResponse) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(data)
}

// Preserve streaming through http.ResponseController when adding access logs.
func (w *loggedResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func logRequests(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		response := &loggedResponse{ResponseWriter: w}
		next.ServeHTTP(response, r)
		status := response.status
		if status == 0 {
			status = http.StatusOK
		}
		level := slog.LevelInfo
		if status >= 500 {
			level = slog.LevelError
		} else if status >= 400 {
			level = slog.LevelWarn
		} else if r.URL.Path == "/healthz" {
			level = slog.LevelDebug
		}
		logger.Log(r.Context(), level, "HTTP request completed", "method", r.Method, "path", r.URL.Path,
			"status", status, "duration", time.Since(started))
	})
}
