package testing

import "context"

// TestCleaner removes public operation, artifact, and report test data without
// touching catalogs, execution/deduplication records, the bus, or host memory.
// Call between tests after their work has settled. Implementations must be safe
// to call repeatedly. Providers can implement CleanContext(context.Context)
// alongside Clean to share a coordinator's deadline.
type TestCleaner interface {
	Clean() error
}

func CleanContext(ctx context.Context, cleaner TestCleaner) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if bounded, ok := cleaner.(interface{ CleanContext(context.Context) error }); ok {
		return bounded.CleanContext(ctx)
	}
	return cleaner.Clean()
}
