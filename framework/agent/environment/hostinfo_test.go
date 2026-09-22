package environment

import (
	"errors"
	"net"
	"slices"
	"testing"
)

// These tests use private discovery helpers to simulate local interfaces and
// lookup failures without depending on the machine that runs the test suite.
func TestResolveDiscoversOnlyMissingHostFields(t *testing.T) {
	for _, test := range []struct {
		name, envIP, envHostname string
		want                     HostInfo
		wantHostname, wantIP     bool
	}{
		{"discover both", "", "", HostInfo{IPAddress: "10.42.0.10", Hostname: "local-pod"}, true, true},
		{"discover hostname", "10.42.0.9", "", HostInfo{IPAddress: "10.42.0.9", Hostname: "local-pod"}, true, false},
		{"discover IP", "", "env-pod", HostInfo{IPAddress: "10.42.0.10", Hostname: "env-pod"}, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("SERVER_IP_ADDRESS", test.envIP)
			t.Setenv("SERVER_HOSTNAME", test.envHostname)
			var hostnameCalled, ipCalled bool
			hostname := func() (string, error) { hostnameCalled = true; return "local-pod", nil }
			ipAddress := func() string { ipCalled = true; return "10.42.0.10" }

			got, err := resolveHostInfo(HostInfo{}, hostname, ipAddress)

			if err != nil || got != test.want {
				t.Fatalf("identity = %+v, error=%v, want %+v", got, err, test.want)
			}
			if hostnameCalled != test.wantHostname || ipCalled != test.wantIP {
				t.Fatalf("discovery called hostname=%t IP=%t, want hostname=%t IP=%t", hostnameCalled, ipCalled, test.wantHostname, test.wantIP)
			}
		})
	}
}

func TestResolveUnavailableLocalInformation(t *testing.T) {
	for _, test := range []struct {
		name     string
		hostname string
		err      error
	}{
		{name: "hostname lookup fails", err: errors.New("unavailable")},
		{name: "invalid local hostname", hostname: "invalid hostname"},
		{name: "empty local hostname"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("SERVER_IP_ADDRESS", "")
			t.Setenv("SERVER_HOSTNAME", "")
			hostname := func() (string, error) { return test.hostname, test.err }
			ipAddress := func() string { return "" }

			got, err := resolveHostInfo(HostInfo{}, hostname, ipAddress)

			if err != nil || got != (HostInfo{}) {
				t.Fatalf("resolution = %+v, %v, want empty identity without error", got, err)
			}
		})
	}
}

func TestServerIPAddressSelection(t *testing.T) {
	address := func(value string) net.Addr { return &net.IPAddr{IP: net.ParseIP(value)} }
	for _, test := range []struct {
		name       string
		interfaces []net.Interface
		addresses  map[string][]net.Addr
		want       string
	}{
		{
			name: "prefer IPv4 across interfaces",
			interfaces: []net.Interface{
				{Index: 2, Name: "eth0", Flags: net.FlagUp},
				{Index: 3, Name: "eth1", Flags: net.FlagUp},
			},
			addresses: map[string][]net.Addr{"eth0": {address("2001:db8::1")}, "eth1": {address("10.42.0.8")}},
			want:      "10.42.0.8",
		},
		{
			name: "exclude down loopback and unusable addresses",
			interfaces: []net.Interface{
				{Index: 1, Name: "lo", Flags: net.FlagUp | net.FlagLoopback},
				{Index: 2, Name: "down"},
				{Index: 3, Name: "eth0", Flags: net.FlagUp},
			},
			addresses: map[string][]net.Addr{
				"lo": {address("10.0.0.1")}, "down": {address("10.0.0.2")},
				"eth0": {address("127.0.0.1"), address("0.0.0.0"), address("169.254.1.2"), address("224.0.0.1"), address("::1"), address("fe80::1"), address("::"), address("ff02::1"), address("fd00::8")},
			},
			want: "fd00::8",
		},
		{
			name: "deterministic interface and numeric IP ordering",
			interfaces: []net.Interface{
				{Index: 3, Name: "eth1", Flags: net.FlagUp},
				{Index: 2, Name: "eth0", Flags: net.FlagUp},
			},
			addresses: map[string][]net.Addr{
				"eth1": {address("10.0.0.1")},
				"eth0": {address("10.42.0.10"), &net.IPNet{IP: net.ParseIP("10.42.0.2"), Mask: net.CIDRMask(24, 32)}},
			},
			want: "10.42.0.2",
		},
		{
			name:       "IPv6-only host",
			interfaces: []net.Interface{{Index: 2, Name: "eth0", Flags: net.FlagUp}},
			addresses:  map[string][]net.Addr{"eth0": {address("2001:db8::a"), address("2001:db8::2")}},
			want:       "2001:db8::2",
		},
		{
			name:       "unavailable interface addresses",
			interfaces: []net.Interface{{Index: 2, Name: "eth0", Flags: net.FlagUp}},
		},
		{name: "no interfaces"},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, ordering := range []string{"original", "reversed"} {
				t.Run(ordering, func(t *testing.T) {
					interfaces := slices.Clone(test.interfaces)
					if ordering == "reversed" {
						slices.Reverse(interfaces)
					}
					addresses := func(iface net.Interface) ([]net.Addr, error) {
						items, ok := test.addresses[iface.Name]
						if !ok {
							return nil, errors.New("interface disappeared")
						}
						items = slices.Clone(items)
						if ordering == "reversed" {
							slices.Reverse(items)
						}
						return items, nil
					}

					got := selectIPAddress(interfaces, addresses)

					if got != test.want {
						t.Fatalf("address = %q, want %q", got, test.want)
					}
				})
			}
		})
	}
}
