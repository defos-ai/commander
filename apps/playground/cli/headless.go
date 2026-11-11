package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/defos-ai/commander/apps/playground/internal/local"
)

func runHeadless(caller context.Context, session *local.Session, output io.Writer) error {
	ready := fmt.Sprintf("Commander ready\nMCP: %s\nAPI: %s\n", session.MCPURL, session.APIURL)
	if session.AdminURL != "" {
		ready += "Admin: " + session.AdminURL + "\n"
	}
	if _, err := io.WriteString(output, ready); err != nil {
		return fmt.Errorf("write Commander readiness: %w", err)
	}
	<-session.Context.Done()
	if caller.Err() != nil || errors.Is(context.Cause(session.Context), local.ErrStopRequested) {
		return nil
	}
	return context.Cause(session.Context)
}
