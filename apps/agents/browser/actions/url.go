package actions

import (
	"errors"
	"net/url"
	"strings"
)

func parseURL(raw string) (*url.URL, error) {
	if raw != strings.TrimSpace(raw) || len(raw) > 2048 {
		return nil, errors.New("url must be at most 2048 characters with no surrounding whitespace")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return nil, errors.New("url must be an absolute HTTP(S) URL without credentials")
	}
	u.Fragment = ""
	if u.Path == "" {
		u.Path = "/"
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	return u, nil
}

// Website scope follows an exact hostname across schemes and ports. A sibling
// or child subdomain is a different hostname, even when it resolves to the same IP.
func sameHostname(a, b *url.URL) bool {
	return a != nil && b != nil && a.Hostname() != "" && strings.EqualFold(a.Hostname(), b.Hostname())
}
