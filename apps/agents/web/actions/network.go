package actions

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"time"
)

type networkPolicy struct {
	hostname  string
	addresses []netip.Addr
	dial      func(context.Context, string, string) (net.Conn, error)
}

func lookupWebsite(ctx context.Context, hostname string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, "ip", hostname)
}

func (policy networkPolicy) transport() *http.Transport {
	return &http.Transport{
		DialContext:           policy.dialContext,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		IdleConnTimeout:       30 * time.Second,
		MaxIdleConns:          10,
	}
}

func resolveNetwork(ctx context.Context, hostname string, lookup func(context.Context, string) ([]netip.Addr, error)) (networkPolicy, error) {
	var addresses []netip.Addr
	if ip, err := netip.ParseAddr(hostname); err == nil {
		addresses = []netip.Addr{ip}
	} else {
		addresses, err = lookup(ctx, hostname)
		if err != nil {
			return networkPolicy{}, fmt.Errorf("resolve website: %w", err)
		}
	}
	if len(addresses) == 0 {
		return networkPolicy{}, errors.New("website has no IP addresses")
	}
	for _, ip := range addresses {
		if !allowedAddress(ip) {
			return networkPolicy{}, errors.New("website resolves to a blocked metadata, link-local or reserved address")
		}
	}
	return networkPolicy{
		hostname:  hostname,
		addresses: slices.Clone(addresses),
		dial:      (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
	}, nil
}

func (policy networkPolicy) dialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil || !strings.EqualFold(host, policy.hostname) {
		return nil, errors.New("connection leaves website hostname scope")
	}
	// Dial the checked IP directly: resolving the hostname again would permit DNS rebinding.
	for _, ip := range policy.addresses {
		var conn net.Conn
		conn, err = policy.dial(ctx, network, net.JoinHostPort(ip.Unmap().String(), port))
		if err == nil {
			return conn, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return nil, err
}

func allowedAddress(ip netip.Addr) bool {
	if !ip.IsValid() || ip.Zone() != "" {
		return false
	}
	ip = ip.Unmap()
	if (!ip.IsGlobalUnicast() && !ip.IsLoopback()) || reservedAddress(ip) {
		return false
	}
	return true
}

var reservedNetworks = []netip.Prefix{
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
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("3fff::/20"),
	netip.MustParsePrefix("fd00:ec2::254/128"),
}

func reservedAddress(ip netip.Addr) bool {
	if ip.Is6() && !ip.IsPrivate() && !ip.IsLoopback() && !netip.MustParsePrefix("2000::/3").Contains(ip) {
		return true
	}
	return slices.ContainsFunc(reservedNetworks, func(prefix netip.Prefix) bool { return prefix.Contains(ip) })
}
