package connections

import (
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path"
	"strconv"
	"strings"
)

func (h *Handler) resolveTarget(base *url.URL) (*url.URL, *http.Client) {
	port := base.Port()
	if port == "" {
		port = "80"
		if base.Scheme == "https" {
			port = "443"
		}
	}
	if upstream, ok := h.apiUpstreams[normalizedHostPort(base.Hostname(), port)]; ok {
		result := *base
		result.Host = upstream
		return &result, h.mappedClient
	}
	return base, h.client
}

func parseAPIUpstreams(value string) (map[string]string, error) {
	result := make(map[string]string)
	if strings.TrimSpace(value) == "" {
		return result, nil
	}
	for _, rule := range strings.Split(value, ",") {
		key, target, ok := strings.Cut(rule, "=")
		if !ok {
			return nil, errors.New("expected comma-separated host:port=host:port pairs")
		}
		key, keyErr := normalizeUpstreamHost(strings.TrimSpace(key))
		target, targetErr := normalizeUpstreamHost(strings.TrimSpace(target))
		if keyErr != nil || targetErr != nil {
			return nil, errors.New("each upstream key and value must be a host:port without scheme, credentials, path, query or fragment")
		}
		if _, exists := result[key]; exists {
			return nil, errors.New("duplicate upstream host:port")
		}
		result[key] = target
	}
	return result, nil
}

func normalizeUpstreamHost(value string) (string, error) {
	host, port, err := net.SplitHostPort(value)
	number, portErr := strconv.Atoi(port)
	base, baseErr := normalizeBase("http://" + value)
	if err != nil || host == "" || portErr != nil || baseErr != nil || base.Path != "" ||
		(strings.HasPrefix(value, "[") && net.ParseIP(host) == nil) {
		return "", errors.New("invalid upstream host:port")
	}
	return normalizedHostPort(base.Hostname(), strconv.Itoa(number)), nil
}

func normalizedHostPort(host, port string) string {
	if address, err := netip.ParseAddr(host); err == nil {
		host = address.String()
	}
	return net.JoinHostPort(host, port)
}

func normalizeBase(value string) (*url.URL, error) {
	u, err := url.Parse(value)
	if err != nil || len(value) > 8192 || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" ||
		u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" ||
		strings.ContainsAny(u.Host+u.Path, "\\%\x00\r\n") {
		return nil, errors.New("API URL must be HTTP(S) without credentials, query or fragment")
	}
	if u.Path != "" && path.Clean(u.Path) != strings.TrimSuffix(u.Path, "/") && u.Path != "/" {
		return nil, errors.New("API URL path must not contain traversal")
	}
	hostname, port := strings.ToLower(u.Hostname()), u.Port()
	if port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 0 || number > 65535 {
			return nil, errors.New("API URL port must be an integer between 0 and 65535")
		}
		port = strconv.Itoa(number)
	}
	if (u.Scheme == "http" && port == "80") || (u.Scheme == "https" && port == "443") {
		port = ""
	}
	u.Host = hostname
	if port != "" {
		u.Host = net.JoinHostPort(hostname, port)
	} else if strings.Contains(hostname, ":") {
		u.Host = "[" + hostname + "]"
	}
	u.RawPath = strings.TrimRight(u.EscapedPath(), "/")
	u.Path, _ = url.PathUnescape(u.RawPath)
	return u, nil
}

func normalizeOrigin(value string) (string, error) {
	u, err := normalizeBase(value)
	if err != nil || u.Path != "" {
		return "", errors.New("public-origin must be an HTTP(S) origin without a path")
	}
	if u.Scheme == "http" && !loopbackHost(u.Hostname()) {
		return "", errors.New("public-origin must use HTTPS except on localhost")
	}
	return u.String(), nil
}

func loopbackHost(host string) bool {
	return strings.EqualFold(host, "localhost") || net.ParseIP(host).IsLoopback()
}

func endpoint(base *url.URL, value string) (*url.URL, bool, error) {
	u, err := url.Parse(value)
	if err != nil || len(value) > 8192 || u.Scheme != "" || u.Host != "" || u.User != nil ||
		u.Opaque != "" || u.Fragment != "" || !strings.HasPrefix(u.Path, "/") ||
		strings.ContainsAny(u.Path, "\\%\x00") || path.Clean(u.Path) != u.Path {
		return nil, false, errors.New("invalid API path")
	}
	public := u.Path == "/healthz" || u.Path == "/readyz" || u.Path == "/aboutz"
	if !public && !strings.HasPrefix(u.Path, "/api/v1/") {
		return nil, false, errors.New("path must identify a Commander API endpoint")
	}
	result := *base
	result.Path += u.Path
	result.RawPath = base.EscapedPath() + u.EscapedPath()
	result.RawQuery = u.RawQuery
	return &result, public, nil
}
