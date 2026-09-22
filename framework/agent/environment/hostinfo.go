package environment

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"sort"
	"strings"

	agentmodel "commander/framework/domain/agents"
)

type HostInfo = agentmodel.ServerInfo

// ResolveHostInfo fills missing fields from SERVER_IP_ADDRESS and SERVER_HOSTNAME,
// then local discovery. Explicit nonempty fields take precedence. In Kubernetes,
// these identify the pod; environment overrides can use the Downward API.
// Discovery makes no external network requests and leaves unavailable fields empty.
func ResolveHostInfo(overrides HostInfo) (HostInfo, error) {
	return resolveHostInfo(overrides, os.Hostname, localIPAddress)
}

func resolveHostInfo(info HostInfo, hostname func() (string, error), ipAddress func() string) (HostInfo, error) {
	if err := info.Validate(); err != nil {
		return HostInfo{}, err
	}
	for _, field := range []struct {
		env   string
		value *string
	}{
		{"SERVER_IP_ADDRESS", &info.IPAddress},
		{"SERVER_HOSTNAME", &info.Hostname},
	} {
		if *field.value != "" {
			continue
		}
		value := os.Getenv(field.env)
		if value == "" {
			continue
		}
		*field.value = strings.TrimSpace(value)
		if *field.value == "" {
			return HostInfo{}, fmt.Errorf("%s must not be blank", field.env)
		}
		if err := info.Validate(); err != nil {
			return HostInfo{}, fmt.Errorf("%s: %w", field.env, err)
		}
	}
	if info.Hostname == "" {
		if value, err := hostname(); err == nil {
			value = strings.TrimSpace(value)
			if (HostInfo{Hostname: value}).Validate() == nil {
				info.Hostname = value
			}
		}
	}
	if info.IPAddress == "" {
		info.IPAddress = ipAddress()
	}
	return info, nil
}

func localIPAddress() string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	return selectIPAddress(interfaces, func(iface net.Interface) ([]net.Addr, error) {
		return iface.Addrs()
	})
}

// Prefer IPv4, then IPv6, on an up, non-loopback interface. Interface index,
// name and numeric address order make discovery independent of enumeration
// order. Explicit configuration is authoritative on multihomed hosts and pods.
func selectIPAddress(interfaces []net.Interface, addresses func(net.Interface) ([]net.Addr, error)) string {
	sort.Slice(interfaces, func(i, j int) bool {
		if interfaces[i].Index != interfaces[j].Index {
			return interfaces[i].Index < interfaces[j].Index
		}
		return interfaces[i].Name < interfaces[j].Name
	})
	var fallback string
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		items, err := addresses(iface)
		if err != nil {
			continue
		}
		var ipv4, ipv6 []net.IP
		for _, address := range items {
			var ip net.IP
			switch value := address.(type) {
			case *net.IPNet:
				ip = value.IP
			case *net.IPAddr:
				ip = value.IP
			}
			// Link-local addresses require interface scope and cannot identify
			// an agent to another host; private pod and LAN addresses are valid.
			if !ip.IsGlobalUnicast() {
				continue
			}
			if value := ip.To4(); value != nil {
				ipv4 = append(ipv4, value)
			} else if value := ip.To16(); value != nil {
				ipv6 = append(ipv6, value)
			}
		}
		sort.Slice(ipv4, func(i, j int) bool { return bytes.Compare(ipv4[i], ipv4[j]) < 0 })
		if len(ipv4) > 0 {
			return ipv4[0].String()
		}
		if fallback == "" && len(ipv6) > 0 {
			sort.Slice(ipv6, func(i, j int) bool { return bytes.Compare(ipv6[i], ipv6[j]) < 0 })
			fallback = ipv6[0].String()
		}
	}
	return fallback
}
