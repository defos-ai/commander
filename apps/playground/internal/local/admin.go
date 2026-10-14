package local

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	admincli "github.com/defos-ai/commander/apps/admin/ui/backend/cli"
)

func (s *Session) startAdmin() {
	ctx, cancel := context.WithTimeout(s.Context, 5*time.Second)
	defer cancel()
	address := os.Getenv("UI_LISTEN_ADDRESS")
	if address == "" {
		address = "127.0.0.1:4275"
	}
	args := []string{"--listen-address=" + address}
	if os.Getenv("UI_API_UPSTREAMS") == "" {
		// Permit only this session's API when it uses a non-default local port.
		apiAddress := strings.TrimPrefix(s.APIURL, "http://")
		args = append(args, "--api-upstreams="+apiAddress+"="+apiAddress)
	}
	ready := make(chan net.Addr, 1)
	admin := s.startHost("Admin", false, func(ctx context.Context) error {
		return admincli.RunWithOptions(ctx, args, s.log, admincli.Options{
			Ready: func(address net.Addr) { ready <- address },
		})
	})
	select {
	case address := <-ready:
		s.AdminURL = localHTTPURL(address)
		return
	case <-admin.done:
		// The host records the error; missions continue without Admin.
	case <-ctx.Done():
		admin.stop()
		fmt.Fprintf(s.log, "Admin UI startup: %v\n", ctx.Err())
	}
	s.Notices = append(s.Notices, "Admin UI unavailable; Playground is ready. Use --debug for details.")
}
