package api

import (
	"errors"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
)

type requestOrigin struct {
	scheme string
	host   string
	port   string
}

func (o requestOrigin) same(other requestOrigin) bool {
	return o.scheme == other.scheme && o.host == other.host && o.effectivePort() == other.effectivePort()
}

func (o requestOrigin) effectivePort() string {
	if o.port != "" {
		return o.port
	}
	if o.scheme == "https" {
		return "443"
	}
	return "80"
}

func normalizeHostname(raw string) (string, error) {
	if address, err := netip.ParseAddr(raw); err == nil && address.Zone() == "" {
		return address.String(), nil
	}
	host := strings.ToLower(raw)
	labels := strings.TrimSuffix(host, ".")
	if labels == "" || len(labels) > 253 {
		return "", errors.New("invalid hostname")
	}
	for _, label := range strings.Split(labels, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", errors.New("invalid hostname")
		}
		for _, character := range label {
			if !(character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '-') {
				return "", errors.New("invalid hostname")
			}
		}
	}
	return host, nil
}

func parseAuthority(raw string) (string, string, error) {
	host, port := raw, ""
	if strings.HasPrefix(raw, "[") {
		end := strings.IndexByte(raw, ']')
		if end < 0 {
			return "", "", errors.New("invalid authority")
		}
		host = raw[1:end]
		address, err := netip.ParseAddr(host)
		if err != nil || !address.Is6() || address.Zone() != "" {
			return "", "", errors.New("invalid authority")
		}
		if end+1 < len(raw) {
			if raw[end+1] != ':' {
				return "", "", errors.New("invalid authority")
			}
			port = raw[end+2:]
			if port == "" {
				return "", "", errors.New("invalid authority")
			}
		}
	} else if strings.Contains(raw, ":") {
		var err error
		host, port, err = net.SplitHostPort(raw)
		if err != nil || port == "" {
			return "", "", errors.New("invalid authority")
		}
	}
	if port != "" {
		for _, character := range port {
			if character < '0' || character > '9' {
				return "", "", errors.New("invalid authority")
			}
		}
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return "", "", errors.New("invalid authority")
		}
		port = strconv.Itoa(number)
	}
	host, err := normalizeHostname(host)
	if err != nil {
		return "", "", errors.New("invalid authority")
	}
	return host, port, nil
}

type proxyPolicy struct {
	trusted []netip.Prefix
	mode    string
}

func newProxyPolicy(trusted []string, mode string) (proxyPolicy, error) {
	if mode == "" {
		mode = "none"
	}
	switch mode {
	case "none", "forwarded", "x-forwarded", "x-forwarded-proto":
	default:
		return proxyPolicy{}, errors.New("invalid forwarded header mode")
	}
	policy := proxyPolicy{mode: mode}
	for _, raw := range trusted {
		raw = strings.TrimSpace(raw)
		prefix, err := netip.ParsePrefix(raw)
		if err != nil {
			address, addressErr := netip.ParseAddr(raw)
			if addressErr != nil || address.Zone() != "" {
				return proxyPolicy{}, errors.New("trusted proxies must be IP addresses or CIDRs")
			}
			address = address.Unmap()
			prefix = netip.PrefixFrom(address, address.BitLen())
		} else if prefix.Addr().Is4In6() && prefix.Bits() >= 96 {
			prefix = netip.PrefixFrom(prefix.Addr().Unmap(), prefix.Bits()-96)
		}
		policy.trusted = append(policy.trusted, prefix.Masked())
	}
	if mode != "none" && len(policy.trusted) == 0 {
		return proxyPolicy{}, errors.New("forwarded headers require trusted proxies")
	}
	return policy, nil
}

func (p proxyPolicy) trusts(remoteAddress string) bool {
	host, _, err := net.SplitHostPort(remoteAddress)
	if err != nil {
		return false
	}
	address, err := netip.ParseAddr(host)
	if err != nil || address.Zone() != "" {
		return false
	}
	address = address.Unmap()
	for _, prefix := range p.trusted {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

func (p proxyPolicy) origin(r *http.Request) (requestOrigin, error) {
	host, port, err := parseAuthority(r.Host)
	if err != nil {
		return requestOrigin{}, err
	}
	origin := requestOrigin{scheme: "http", host: host, port: port}
	if r.TLS != nil {
		origin.scheme = "https"
	}
	if p.mode == "none" || !p.trusts(r.RemoteAddr) {
		return origin, nil
	}
	var publicHost, publicScheme string
	if p.mode == "forwarded" {
		value, err := forwardedHeader(r, "Forwarded")
		if err != nil {
			return requestOrigin{}, err
		}
		if value != "" {
			if err := validateForwardedParameters(value); err != nil {
				return requestOrigin{}, err
			}
			_, parameters, err := mime.ParseMediaType("forwarded;" + value)
			if err != nil {
				return requestOrigin{}, errors.New("invalid forwarded header")
			}
			publicHost, publicScheme = parameters["host"], parameters["proto"]
			if host, exists := parameters["host"]; exists && host == "" {
				return requestOrigin{}, errors.New("invalid forwarded host")
			}
			if scheme, exists := parameters["proto"]; exists && scheme == "" {
				return requestOrigin{}, errors.New("invalid forwarded protocol")
			}
		}
	} else {
		publicScheme, err = forwardedHeader(r, "X-Forwarded-Proto")
		if err != nil {
			return requestOrigin{}, err
		}
		if p.mode == "x-forwarded" {
			publicHost, err = forwardedHeader(r, "X-Forwarded-Host")
			if err != nil {
				return requestOrigin{}, err
			}
		}
	}
	if publicHost != "" {
		origin.host, origin.port, err = parseAuthority(publicHost)
		if err != nil {
			return requestOrigin{}, errors.New("invalid forwarded host")
		}
	}
	if publicScheme != "" {
		origin.scheme = strings.ToLower(publicScheme)
		if origin.scheme != "http" && origin.scheme != "https" {
			return requestOrigin{}, errors.New("invalid forwarded protocol")
		}
	}
	return origin, nil
}

func validateForwardedParameters(value string) error {
	names := make(map[string]bool)
	start, quoted, escaped := 0, false, false
	check := func(parameter string) error {
		name, _, found := strings.Cut(parameter, "=")
		name = strings.ToLower(strings.TrimSpace(name))
		if !found || name == "" || strings.Contains(name, "*") || names[name] {
			return errors.New("invalid forwarded parameters")
		}
		names[name] = true
		return nil
	}
	for index, character := range value {
		if escaped {
			escaped = false
			continue
		}
		if quoted && character == '\\' {
			escaped = true
		} else if character == '"' {
			quoted = !quoted
		} else if !quoted && character == ';' {
			if err := check(value[start:index]); err != nil {
				return err
			}
			start = index + 1
		}
	}
	return check(value[start:])
}

func forwardedHeader(r *http.Request, name string) (string, error) {
	values := r.Header.Values(name)
	if len(values) == 0 {
		return "", nil
	}
	if len(values) != 1 || strings.ContainsAny(values[0], ",\r\n") {
		return "", errors.New("forwarded headers must contain a single value")
	}
	value := strings.TrimSpace(values[0])
	if value == "" {
		return "", errors.New("invalid forwarded header")
	}
	for _, character := range value {
		if character < 32 || character == 127 {
			return "", errors.New("invalid forwarded header")
		}
	}
	return value, nil
}
