package cli

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"github.com/defos-ai/commander/apps/playground/internal/local"
)

const defaultAdminURL = "http://localhost:4275"

func validateAdminURL(raw string) error {
	invalid := errors.New("admin-url: enter an HTTP(S) browser address without credentials, query parameters or a fragment")
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" ||
		u.User != nil || u.RawQuery != "" || u.ForceQuery || strings.Contains(raw, "#") ||
		strings.Contains(raw, "\\") || strings.ContainsFunc(raw, unicode.IsSpace) ||
		strings.ContainsFunc(raw, unicode.IsControl) || strings.ContainsFunc(u.Path, unicode.IsControl) {
		return invalid
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && ip.IsUnspecified() {
		return invalid
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return invalid
		}
	} else if strings.HasSuffix(u.Host, ":") {
		return invalid
	}
	return nil
}

func (c *config) configureLocalAdmin(session *local.Session) {
	c.configureAdminServer(session.ServeAdmin)
	if session.AdminURL == "" {
		if c.ServeAdminUI {
			c.AdminURL = ""
			c.AdminHint = "Admin UI unavailable. Use /admin serve to try again."
		} else if c.AdminURL == "" {
			c.AdminHint = "Start Admin with /admin serve."
		} else {
			c.Config.SetAdminURL(c.AdminURL)
		}
		return
	}
	c.Config.SetAdminURL(c.adminBrowserAddress(session.AdminURL))
}

func (c *config) adminBrowserAddress(ready string) string {
	if c.adminOverride != "" {
		return c.adminOverride
	}
	if c.AllInOne && (env("API_RUNTIME", "native") == "docker" || env("API_RUNTIME", "native") == "docker-compose") {
		return defaultAdminURL
	}
	return ready
}

func (c *config) configureAdminServer(start func(context.Context, []string) (string, error)) {
	// Capture the operator's plain browser override before adding prefill data.
	settings := *c
	c.ServeAdmin = func(ctx context.Context, args []string) (string, error) {
		address, err := start(ctx, args)
		if err != nil {
			return "", err
		}
		return settings.adminBrowserAddress(address), nil
	}
}
