package host

import "os"

// Value selects an explicit flag, a nonempty environment variable, or the
// fallback, in that order. Explicit empty flags are preserved for validation.
// Resolve after parsing flags so help output never embeds environment secrets.
func Value(flagValue string, flagProvided bool, envName, fallback string) string {
	if flagProvided {
		return flagValue
	}
	if value := os.Getenv(envName); value != "" {
		return value
	}
	return fallback
}
