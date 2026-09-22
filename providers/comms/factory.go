package comms

import (
	"context"

	"commander/framework/comms"
	fscomms "commander/providers/fs/comms"
	"commander/providers/internal/connections"
)

// OpenBus opens a broker selected by its URL. Currently, local paths and file://
// or fs:// URLs are supported. The caller closes the bus after its consumers stop.
func OpenBus(ctx context.Context, rawURL string) (comms.Bus, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := connections.FilesystemPath(rawURL)
	if err != nil {
		return nil, err
	}
	return fscomms.NewFsBus(root)
}
