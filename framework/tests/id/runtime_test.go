package id_test

import (
	"strings"
	"testing"

	"commander/framework/id"
)

func TestRuntimeIdentity(t *testing.T) {
	for _, tc := range []struct {
		runtimeType string
		role        id.Role
	}{
		{runtimeType: "web-quality", role: id.Agent},
		{runtimeType: "default", role: id.Controller},
		{runtimeType: "mcp", role: id.Adapter},
		{runtimeType: "scanner-v2", role: id.Agent},
	} {
		t.Run(string(tc.role), func(t *testing.T) {
			// Arrange.
			prefix := tc.runtimeType + "-" + string(tc.role) + "-"

			// Act.
			value, err := id.NewRuntime(tc.runtimeType, tc.role)
			runtimeType, valid := id.RuntimeType(value, tc.role)

			// Assert.
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(value, prefix) || len(value) != len(prefix)+8 {
				t.Fatalf("expected %s followed by eight hex characters, got %q", prefix, value)
			}
			for _, c := range strings.TrimPrefix(value, prefix) {
				if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
					t.Fatalf("expected lowercase hex suffix, got %q", value)
				}
			}
			if !valid || runtimeType != tc.runtimeType || !id.ValidRuntime(value, tc.role) {
				t.Fatalf("expected type %q, got %q (valid=%v)", tc.runtimeType, runtimeType, valid)
			}
			if id.Valid(value) {
				t.Fatalf("runtime ID must not be accepted as a resource UUID: %q", value)
			}
		})
	}
}

func TestRuntimeIdentityValidation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		role  id.Role
		valid bool
	}{
		{name: "agent", value: "web-quality-agent-0123abcd", role: id.Agent, valid: true},
		{name: "controller", value: "default-controller-653f51ad", role: id.Controller, valid: true},
		{name: "adapter", value: "mcp-adapter-0123abcd", role: id.Adapter, valid: true},
		{name: "role suffix within type", value: "custom-agent-agent-0123abcd", role: id.Agent, valid: true},
		{name: "role mismatch", value: "mcp-adapter-0123abcd", role: id.Agent},
		{name: "unknown role", value: "custom-worker-0123abcd", role: id.Role("worker")},
		{name: "empty", role: id.Agent},
		{name: "missing type", value: "-agent-0123abcd", role: id.Agent},
		{name: "leading hyphen", value: "-cyber-agent-0123abcd", role: id.Agent},
		{name: "trailing type hyphen", value: "cyber--agent-0123abcd", role: id.Agent},
		{name: "consecutive type hyphens", value: "cyber--security-agent-0123abcd", role: id.Agent},
		{name: "uppercase type", value: "Cyber-agent-0123abcd", role: id.Agent},
		{name: "uppercase suffix", value: "cyber-agent-0123ABCd", role: id.Agent},
		{name: "nonhex suffix", value: "cyber-agent-1ksda123", role: id.Agent},
		{name: "short suffix", value: "cyber-agent-123abcd", role: id.Agent},
		{name: "long suffix", value: "cyber-agent-00123abcd", role: id.Agent},
		{name: "path traversal", value: "../cyber-agent-0123abcd", role: id.Agent},
		{name: "absolute path", value: "/cyber-agent-0123abcd", role: id.Agent},
		{name: "windows path", value: "cyber\\security-agent-0123abcd", role: id.Agent},
		{name: "routing separator", value: "cyber.security-agent-0123abcd", role: id.Agent},
		{name: "routing wildcard", value: "cyber*-agent-0123abcd", role: id.Agent},
		{name: "encoded separator", value: "cyber%2fsecurity-agent-0123abcd", role: id.Agent},
		{name: "unicode", value: "cybér-agent-0123abcd", role: id.Agent},
		{name: "whitespace", value: " cyber-agent-0123abcd", role: id.Agent},
		{name: "uuid", value: "59a18399-be7a-4c2b-9c08-987621fdfc60", role: id.Agent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange.
			value, role := tc.value, tc.role

			// Act.
			valid := id.ValidRuntime(value, role)
			runtimeType, extracted := id.RuntimeType(value, role)

			// Assert.
			if valid != tc.valid || extracted != tc.valid {
				t.Fatalf("expected valid=%v, got ValidRuntime=%v and RuntimeType=%v for %q", tc.valid, valid, extracted, value)
			}
			if !tc.valid && runtimeType != "" {
				t.Fatalf("invalid ID returned a type: %q", runtimeType)
			}
		})
	}
}

func TestRuntimeIdentityLengthLimit(t *testing.T) {
	for _, role := range []id.Role{id.Agent, id.Controller, id.Adapter} {
		t.Run(string(role), func(t *testing.T) {
			// Arrange.
			maxType := strings.Repeat("a", 63-len(role)-10)

			// Act.
			value, err := id.NewRuntime(maxType, role)
			tooLong, lengthErr := id.NewRuntime(maxType+"a", role)

			// Assert.
			if err != nil || len(value) != 63 || !id.ValidRuntime(value, role) {
				t.Fatalf("expected a valid 63-character ID, got %q: %v", value, err)
			}
			if lengthErr == nil || tooLong != "" {
				t.Fatalf("expected an error for oversized type, got %q: %v", tooLong, lengthErr)
			}
			if id.ValidRuntime("a"+value, role) {
				t.Fatal("accepted an oversized runtime ID")
			}
		})
	}
}

func TestNewRuntimeRejectsInvalidTypes(t *testing.T) {
	for _, runtimeType := range []string{"", "Cyber", " cyber", "cyber ", "-cyber", "cyber-", "cyber--security", "../cyber", "cyber/security", "cyber.security", "cyber_security", "cybér"} {
		t.Run(runtimeType, func(t *testing.T) {
			// Arrange.
			role := id.Agent

			// Act.
			value, err := id.NewRuntime(runtimeType, role)

			// Assert.
			if err == nil || value != "" {
				t.Fatalf("expected invalid type %q to fail, got %q: %v", runtimeType, value, err)
			}
		})
	}
}

func TestNewRuntimeRejectsUnknownRole(t *testing.T) {
	// Arrange.
	role := id.Role("worker")

	// Act.
	value, err := id.NewRuntime("default", role)

	// Assert.
	if err == nil || value != "" {
		t.Fatalf("expected unknown role to fail, got %q: %v", value, err)
	}
}

func TestResourceUUIDRemainsIndependent(t *testing.T) {
	// Arrange.
	roles := []id.Role{id.Agent, id.Controller, id.Adapter}

	// Act.
	value := id.New()

	// Assert.
	if !id.Valid(value) {
		t.Fatalf("expected canonical resource UUID, got %q", value)
	}
	for _, role := range roles {
		if id.ValidRuntime(value, role) {
			t.Fatalf("resource UUID accepted as a runtime ID for %s", role)
		}
	}
}

func TestRuntimeTypeValidation(t *testing.T) {
	for _, tc := range []struct {
		value string
		valid bool
	}{
		{value: "default", valid: true},
		{value: "web-quality", valid: true},
		{value: "scanner-v2", valid: true},
		{value: strings.Repeat("a", 63), valid: true},
		{value: ""},
		{value: "Cyber"},
		{value: "cyber--security"},
		{value: "cyber.security"},
		{value: "../cyber"},
		{value: "cyber/security"},
		{value: "cyber_security"},
		{value: "cyber*"},
		{value: "cyber#"},
		{value: "cyber%2fsecurity"},
		{value: "cyber security"},
		{value: "cyber\n"},
		{value: "cybér"},
		{value: strings.Repeat("a", 64)},
	} {
		t.Run(tc.value, func(t *testing.T) {
			// Arrange.
			value := tc.value

			// Act.
			valid := id.ValidType(value)

			// Assert.
			if valid != tc.valid {
				t.Fatalf("expected ValidType(%q)=%v, got %v", value, tc.valid, valid)
			}
		})
	}
}

func TestRuntimeIdentityRoles(t *testing.T) {
	for _, tc := range []struct {
		name    string
		value   string
		agent   bool
		adapter bool
	}{
		{name: "UUID", value: "59a18399-be7a-4c2b-9c08-987621fdfc60"},
		{name: "agent", value: "web-quality-agent-0123abcd", agent: true},
		{name: "adapter", value: "mcp-adapter-0123abcd", adapter: true},
		{name: "controller role mismatch", value: "default-controller-0123abcd"},
		{name: "uppercase UUID", value: "59A18399-be7a-4c2b-9c08-987621fdfc60"},
		{name: "path traversal", value: "../cyber-agent-0123abcd"},
		{name: "routing separator", value: "cyber.security-agent-0123abcd"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange.
			value := tc.value

			// Act.
			agent := id.ValidAgent(value)
			adapter := id.ValidAdapter(value)

			// Assert.
			if agent != tc.agent || adapter != tc.adapter {
				t.Fatalf("expected agent=%v adapter=%v, got agent=%v adapter=%v for %q", tc.agent, tc.adapter, agent, adapter, value)
			}
			if id.Valid(value) && (id.ValidRuntime(value, id.Agent) || id.ValidRuntime(value, id.Adapter)) {
				t.Fatal("UUID accepted by strict runtime ID validation")
			}
		})
	}
}
