package connections

import "testing"

func TestAPIUpstreamHostMapping(t *testing.T) {
	handler, err := NewHandler(Config{APIUpstreams: "EXAMPLE.COM:00080=api:14274,example.com:443=secure-api:8443,[0:0:0:0:0:0:0:1]:4274=[::2]:14274,api:14274=next:4274"})
	if err != nil {
		t.Fatal(err)
	}
	defer handler.Close()
	for _, test := range []struct{ input, want string }{
		{"http://Example.COM:00080/root%2Fapi/", "http://api:14274/root%2Fapi"},
		{"https://example.com/root", "https://secure-api:8443/root"},
		{"http://[::1]:4274/base", "http://[::2]:14274/base"},
		{"http://[0:0:0:0:0:0:0:1]:4274/base", "http://[::2]:14274/base"},
		{"http://example.com:8080/root", "http://example.com:8080/root"},
		{"http://elsewhere:4274/root", "http://elsewhere:4274/root"},
	} {
		t.Run(test.input, func(t *testing.T) {
			base, err := normalizeBase(test.input)
			if err != nil {
				t.Fatal(err)
			}
			publicURL := base.String()
			resolved := handler.resolveTarget(base)
			if resolved.String() != test.want || base.String() != publicURL {
				t.Fatalf("mapping must preserve the public URL and rewrite its host once: got %s, want %s, public %s", resolved, test.want, base)
			}
		})
	}
}
