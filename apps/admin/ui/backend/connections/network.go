package connections

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"
)

var errUnsafeTarget = errors.New("API address is not allowed")

type apiDialer struct {
	mapped bool
	lookup func(context.Context, string, string) ([]netip.Addr, error)
	dial   func(context.Context, string, string) (net.Conn, error)
}

func newAPIClient(mapped bool) *http.Client {
	dialer := apiDialer{
		mapped: mapped,
		lookup: net.DefaultResolver.LookupNetIP,
		dial:   (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
	}
	return &http.Client{
		Timeout:       30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{
			DialContext: dialer.DialContext,
			// Environment proxies can resolve targets outside this address policy.
			Proxy:               nil,
			TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 25 * time.Second,
			IdleConnTimeout: 90 * time.Second, MaxIdleConns: 32, MaxIdleConnsPerHost: 8, MaxConnsPerHost: 64,
		},
	}
}

func (d apiDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, errUnsafeTarget
	}
	var ips []netip.Addr
	if ip, err := netip.ParseAddr(host); err == nil {
		ips = []netip.Addr{ip}
	} else {
		ips, err = d.lookup(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
	}
	local := port == "4274" && (strings.EqualFold(host, "localhost") || host == "127.0.0.1" || host == "::1")
	if len(ips) == 0 {
		return nil, errUnsafeTarget
	}
	for _, ip := range ips {
		if !allowedAPIAddress(ip, d.mapped, local) {
			return nil, errUnsafeTarget
		}
	}
	for _, ip := range ips {
		// Dial the checked IP, never re-resolve a hostname after validating it.
		var connection net.Conn
		connection, err = d.dial(ctx, network, net.JoinHostPort(ip.Unmap().String(), port))
		if err == nil {
			return connection, nil
		}
	}
	return nil, err
}

var blockedAPINetworks = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("168.63.129.16/32"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("3fff::/20"),
	netip.MustParsePrefix("fd00:ec2::254/128"),
}

func allowedAPIAddress(ip netip.Addr, mapped, local bool) bool {
	if !ip.IsValid() || ip.Zone() != "" {
		return false
	}
	ip = ip.Unmap()
	if ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return false
	}
	for _, prefix := range blockedAPINetworks {
		if prefix.Contains(ip) {
			return false
		}
	}
	if local && !mapped {
		return ip.IsLoopback()
	}
	if ip.IsLoopback() {
		return mapped
	}
	if ip.IsPrivate() {
		return mapped
	}
	return ip.IsGlobalUnicast() && (ip.Is4() || netip.MustParsePrefix("2000::/3").Contains(ip))
}

func failUnsafeTarget(w http.ResponseWriter, err error) bool {
	if !errors.Is(err, errUnsafeTarget) {
		return false
	}
	fail(w, http.StatusForbidden, "API address is blocked. Configure UI_API_UPSTREAMS for an internal API.")
	return true
}
