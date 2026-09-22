package agents

import (
	"errors"
	"net"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ServerInfo identifies the host or container network namespace running an
// agent. In Kubernetes these are the pod IP and hostname, not the node address.
// Empty fields represent information unavailable locally.
type ServerInfo struct {
	IPAddress string `json:"ip_address"`
	Hostname  string `json:"hostname"`
}

func (s ServerInfo) Validate() error {
	if s.IPAddress != "" && net.ParseIP(s.IPAddress) == nil {
		return errors.New("server IP address must be an IPv4 or IPv6 address")
	}
	if !utf8.ValidString(s.Hostname) {
		return errors.New("server hostname must contain valid UTF-8")
	}
	if s.Hostname != "" && (strings.ContainsAny(s.Hostname, `/\`) || strings.IndexFunc(s.Hostname, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) >= 0) {
		return errors.New("server hostname must not contain whitespace, control characters or path separators")
	}
	return nil
}
