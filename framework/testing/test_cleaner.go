// Package testing defines helpers for isolated test environments.
package testing

// TestCleaner removes persisted test data without touching the message bus or
// resetting host memory. Call between tests after their work has settled.
// Implementations must be safe to call repeatedly.
type TestCleaner interface {
	Clean() error
}
