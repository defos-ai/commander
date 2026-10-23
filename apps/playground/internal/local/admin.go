package local

import "context"

// ServeAdmin starts the optional UI without tying its lifetime to command startup.
func (s *Session) ServeAdmin(ctx context.Context, args []string) (string, error) {
	return s.admin.Start(ctx, args)
}
