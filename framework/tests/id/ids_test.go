package id_test

import (
	"regexp"
	"testing"

	"commander/framework/id"
)

func TestRuntimeIDGenerators(t *testing.T) {
	for _, test := range []struct {
		name, kind, pattern string
		generate            func(string) (string, error)
	}{
		{"agent", "web-quality", `^web-quality-agent-[0-9a-f]{8}$`, id.AgentID},
		{"controller", "default", `^default-controller-[0-9a-f]{8}$`, id.ControllerID},
		{"adapter", "mcp", `^mcp-adapter-[0-9a-f]{8}$`, id.AdapterID},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			format := regexp.MustCompile(test.pattern)

			// Act.
			value, err := test.generate(test.kind)
			invalid, invalidErr := test.generate("../invalid")

			// Assert.
			if err != nil || !format.MatchString(value) {
				t.Fatalf("unexpected %s ID %q: %v", test.name, value, err)
			}
			if invalidErr == nil || invalid != "" {
				t.Fatalf("invalid runtime type produced %q: %v", invalid, invalidErr)
			}
		})
	}
}

func TestResourceIDGenerators(t *testing.T) {
	for _, test := range []struct {
		name     string
		generate func() string
	}{
		{"message", id.MessageID},
		{"operation", id.OperationID},
		{"task", id.TaskID},
		{"question", id.QuestionID},
		{"report", id.ReportID},
		{"artifact", id.ArtifactID},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			format := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

			// Act.
			first, second := test.generate(), test.generate()

			// Assert.
			if !format.MatchString(first) || !format.MatchString(second) || first == second {
				t.Fatalf("expected distinct UUIDv4 %s IDs, got %q and %q", test.name, first, second)
			}
		})
	}
}
