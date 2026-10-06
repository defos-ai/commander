package connections

import (
	"errors"
	"net"
	"net/url"
	"path"
	"strings"
)

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
	public := u.Path == "/healthz" || u.Path == "/readyz"
	if !public && !strings.HasPrefix(u.Path, "/api/v1/") {
		return nil, false, errors.New("path must identify a Commander API endpoint")
	}
	result := *base
	result.Path += u.Path
	result.RawPath = base.EscapedPath() + u.EscapedPath()
	result.RawQuery = u.RawQuery
	return &result, public, nil
}
