package actions

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"reflect"
	"testing"
)

func TestWebsiteAddressPolicy(t *testing.T) {
	for _, address := range []string{"8.8.8.8", "2606:4700:4700::1111", "10.0.0.1", "172.16.0.1", "192.168.1.1", "127.0.0.1", "::1", "fd12::1", "::ffff:127.0.0.1"} {
		if !allowedAddress(netip.MustParseAddr(address)) {
			t.Errorf("authorized target blocked: %s", address)
		}
	}
	for _, address := range []string{"169.254.169.254", "169.254.170.2", "100.100.100.200", "168.63.129.16", "fd00:ec2::254", "::ffff:169.254.169.254", "fe80::1", "fe80::1%lo", "0.0.0.0", "::", "224.0.0.1", "ff02::1", "240.0.0.1", "192.0.2.1", "198.18.0.1", "2001:db8::1", "64:ff9b::a9fe:a9fe", "2002:a9fe:a9fe::1"} {
		if allowedAddress(netip.MustParseAddr(address)) {
			t.Errorf("unsafe target allowed: %s", address)
		}
	}
}

func TestWebsitePinsDNSAcrossRequestsAndPortChanges(t *testing.T) {
	resolutions := 0
	answers := []netip.Addr{netip.MustParseAddr("8.8.8.8")}
	policy, err := resolveNetwork(t.Context(), "site.test", func(context.Context, string) ([]netip.Addr, error) {
		resolutions++
		return answers, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	answers[0] = netip.MustParseAddr("169.254.169.254")
	var dialed []string
	dialError := errors.New("test connection")
	policy.dial = func(_ context.Context, _, address string) (net.Conn, error) {
		dialed = append(dialed, address)
		return nil, dialError
	}
	for _, address := range []string{"site.test:80", "site.test:443", "site.test:8080"} {
		if _, err := policy.dialContext(t.Context(), "tcp", address); !errors.Is(err, dialError) {
			t.Fatal(err)
		}
	}
	if resolutions != 1 || !reflect.DeepEqual(dialed, []string{"8.8.8.8:80", "8.8.8.8:443", "8.8.8.8:8080"}) {
		t.Fatalf("resolution/dial changed: %d %v", resolutions, dialed)
	}
	if _, err := policy.dialContext(t.Context(), "tcp", "other.test:80"); err == nil || len(dialed) != 3 {
		t.Fatalf("other host reached dialer: %v %v", err, dialed)
	}
}

func TestWebsiteRejectsMixedDNSAnswers(t *testing.T) {
	_, err := resolveNetwork(t.Context(), "site.test", func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("169.254.169.254")}, nil
	})
	if err == nil {
		t.Fatal("mixed metadata answer accepted")
	}
}

func TestWebsiteIgnoresEnvironmentProxy(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("used environment proxy") }))
	defer proxy.Close()
	t.Setenv("HTTP_PROXY", proxy.URL)
	t.Setenv("HTTPS_PROXY", proxy.URL)
	t.Setenv("NO_PROXY", "")
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "<title>Site</title>") }))
	defer site.Close()
	policy, err := resolveNetwork(t.Context(), "site.test", func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	target, _ := url.Parse(site.URL)
	policy.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "8.8.8.8:80" {
			return nil, fmt.Errorf("unexpected destination: %s", address)
		}
		return (&net.Dialer{}).DialContext(ctx, network, target.Host)
	}
	client := &http.Client{Transport: policy.transport()}
	defer client.CloseIdleConnections()
	request, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://site.test/", nil)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
}

func TestRedirectHostnameScopeAcrossSchemes(t *testing.T) {
	scope, _ := parseURL("https://127.0.0.1/")
	client, err := httpClient(t.Context(), scope)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	for _, destination := range []string{"http://127.0.0.1/", "https://127.0.0.1:8443/", "http://127.0.0.1:8080/"} {
		u, _ := url.Parse(destination)
		if err := client.CheckRedirect(&http.Request{URL: u}, nil); err != nil {
			t.Fatalf("valid scheme/port change: %v", err)
		}
	}
	scope.Host = "site.test"
	for _, destination := range []string{"https://www.site.test/", "https://other.test/"} {
		u, _ := url.Parse(destination)
		if err := client.CheckRedirect(&http.Request{URL: u}, nil); err == nil {
			t.Fatalf("accepted different hostname: %s", destination)
		}
	}
}
