package cli

import (
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"github.com/defos-ai/commander/apps/playground/internal/local"
)

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
	if c.NoAdmin {
		c.AdminURL = ""
		c.AdminHint = "Admin is disabled. Start without --no-admin to enable it."
		return
	}
	if session.AdminURL == "" {
		c.AdminURL = ""
		c.AdminHint = "Admin UI is unavailable. Use --debug for details."
		return
	}
	if c.AdminURL == "" {
		switch env("API_RUNTIME", "native") {
		case "docker", "docker-compose":
			// A listener inside a container does not tell us its published host port.
			c.AdminHint = "Publish Admin port 4275 and set --admin-url to its browser address."
			return
		}
		c.AdminURL = session.AdminURL
	}
	// These values only prefill the form; authentication still requires a password.
	prefill := url.Values{"api": {session.APIURL}, "username": {session.APICredentials.Username}}
	c.AdminURL = strings.TrimRight(c.AdminURL, "/") + "/#/connect?" + prefill.Encode()
}
