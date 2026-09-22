package configenv_test

import (
	"testing"

	"commander/framework/host"
)

func TestValuePrecedence(t *testing.T) {
	for _, test := range []struct {
		name     string
		flag     string
		provided bool
		env      string
		want     string
	}{
		{"flag overrides environment", "file:///cli", true, "file:///environment", "file:///cli"},
		{"explicit empty flag remains empty", "", true, "file:///environment", ""},
		{"environment overrides default", "file:///default", false, "file:///environment", "file:///environment"},
		{"empty environment uses default", "", false, "", "file:///default"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			const variable = "COMMANDER_TEST_CONNECTION_URL"
			t.Setenv(variable, test.env)

			// Act.
			got := host.Value(test.flag, test.provided, variable, "file:///default")

			// Assert.
			if got != test.want {
				t.Fatalf("value = %q, want %q", got, test.want)
			}
		})
	}
}
