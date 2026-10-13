package connections

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
)

func TestAPIAddressPolicy(t *testing.T) {
	for _, test := range []struct {
		address       string
		mapped, local bool
		allowed       bool
	}{
		{address: "8.8.8.8", allowed: true},
		{address: "2606:4700:4700::1111", allowed: true},
		{address: "10.1.2.3"},
		{address: "172.16.0.1"},
		{address: "192.168.1.1"},
		{address: "fd00::1"},
		{address: "127.0.0.1"},
		{address: "::ffff:127.0.0.1"},
		{address: "::1"},
		{address: "10.1.2.3", mapped: true, allowed: true},
		{address: "fd00::1", mapped: true, allowed: true},
		{address: "127.0.0.1", mapped: true, allowed: true},
		{address: "127.0.0.1", local: true, allowed: true},
		{address: "::1", local: true, allowed: true},
		{address: "10.1.2.3", local: true},
		{address: "8.8.8.8", local: true},
	} {
		t.Run(test.address, func(t *testing.T) {
			if got := allowedAPIAddress(netip.MustParseAddr(test.address), test.mapped, test.local); got != test.allowed {
				t.Fatalf("allowed=%v, want %v (mapped=%v, local=%v)", got, test.allowed, test.mapped, test.local)
			}
		})
	}
	for _, value := range []string{
		"0.0.0.0", "0.1.2.3", "::", "::2", "169.254.169.254", "169.254.170.2", "100.100.100.200",
		"fd00:ec2::254", "fe80::1", "fe80::1%lo", "224.0.0.1", "ff02::1", "255.255.255.255",
		"168.63.129.16", "192.0.2.1", "2001:2::1", "2001:db8::1", "3fff::1", "64:ff9b::a9fe:a9fe", "2002:a9fe:a9fe::1",
		"::ffff:127.0.0.1%lo", "::ffff:8.8.8.8%lo",
	} {
		t.Run("never-"+value, func(t *testing.T) {
			if allowedAPIAddress(netip.MustParseAddr(value), true, true) {
				t.Fatal("unsafe address must stay blocked even for a configured mapping")
			}
		})
	}
}

func TestAPIDialPinsDNSAndRejectsRebinding(t *testing.T) {
	lookups := 0
	dials := 0
	dialer := apiDialer{
		lookup: func(_ context.Context, network, host string) ([]netip.Addr, error) {
			lookups++
			if network != "ip" || host != "commander.example" {
				t.Fatal("unexpected DNS query")
			}
			if lookups == 1 {
				return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
			}
			return []netip.Addr{netip.MustParseAddr("10.0.0.1")}, nil
		},
		dial: func(_ context.Context, _, address string) (net.Conn, error) {
			dials++
			if address != "8.8.8.8:443" {
				t.Fatalf("dial did not use the validated numeric IP: %s", address)
			}
			client, server := net.Pipe()
			server.Close()
			return client, nil
		},
	}
	connection, err := dialer.DialContext(t.Context(), "tcp", "commander.example:443")
	if err != nil {
		t.Fatal(err)
	}
	connection.Close()
	if lookups != 1 || dials != 1 {
		t.Fatal("dial must resolve once and connect directly")
	}
	if _, err := dialer.DialContext(t.Context(), "tcp", "commander.example:443"); !errors.Is(err, errUnsafeTarget) {
		t.Fatalf("rebound private IP accepted: %v", err)
	}
	if dials != 1 {
		t.Fatal("rebinding reached the dialer")
	}
}

func TestAPIDialRejectsMixedDNSAndUnexpectedLocalAddresses(t *testing.T) {
	for _, test := range []struct {
		target string
		ips    []string
	}{
		{"commander.example:443", []string{"8.8.8.8", "127.0.0.1"}},
		{"localhost:4274", []string{"10.0.0.1"}},
		{"localhost:4274", []string{"8.8.8.8"}},
		{"localhost:8080", []string{"127.0.0.1"}},
		{"127.0.0.2:4274", nil},
		{"[::ffff:8.8.8.8%lo]:4274", nil},
	} {
		t.Run(test.target, func(t *testing.T) {
			dialer := apiDialer{
				lookup: func(context.Context, string, string) ([]netip.Addr, error) {
					var result []netip.Addr
					for _, value := range test.ips {
						result = append(result, netip.MustParseAddr(value))
					}
					return result, nil
				},
				dial: func(context.Context, string, string) (net.Conn, error) {
					t.Fatal("unsafe resolved address reached dial")
					return nil, nil
				},
			}
			if _, err := dialer.DialContext(t.Context(), "tcp", test.target); !errors.Is(err, errUnsafeTarget) {
				t.Fatalf("unsafe DNS answers accepted: %v", err)
			}
		})
	}
}
