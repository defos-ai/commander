package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/defos-ai/commander/apps/admin/client"
	adminsessions "github.com/defos-ai/commander/apps/admin/sessions"
	"github.com/defos-ai/commander/framework/id"
)

func runSessions(ctx context.Context, configuration config, stdout io.Writer) error {
	args := configuration.arguments[1:]
	if len(args) == 0 || len(args) == 1 && args[0] == "help" {
		return sessionHelp(stdout)
	}
	if err := validateSessionArguments(args, configuration); err != nil {
		return &UsageError{cause: err}
	}
	api, err := client.New(configuration.connection)
	if err != nil {
		return &UsageError{cause: err}
	}
	defer api.Close()
	if args[0] == "list" {
		items, err := adminsessions.List(ctx, api, adminsessions.Filter{ScopeID: configuration.scopeID, OwnerAgentID: configuration.ownerID})
		if err != nil {
			return err
		}
		if configuration.json {
			return writeJSON(stdout, struct {
				Items []adminsessions.Session `json:"items"`
			}{Items: items})
		}
		if len(items) == 0 {
			_, err := fmt.Fprintln(stdout, "No sessions.")
			return err
		}
		for _, item := range items {
			if _, err := fmt.Fprintf(stdout, "%s · %s · %s · %s\n", item.ID, item.State, item.Disposition, sessionOwner(item)); err != nil {
				return err
			}
		}
		return nil
	}
	if args[0] == "inspect" {
		item, err := adminsessions.Get(ctx, api, args[1])
		if err != nil {
			return err
		}
		if configuration.json {
			return writeJSON(stdout, item)
		}
		return showSession(stdout, item)
	}
	result, err := adminsessions.Control(ctx, api, args[1], args[0], configuration.requestID)
	if err != nil {
		return err
	}
	if configuration.json {
		return writeJSON(stdout, result)
	}
	if err := showSession(stdout, result.Session); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "Submitted request: %s\n", result.RequestID)
	return err
}

func validateSessionArguments(args []string, configuration config) error {
	invalid := errors.New("invalid session command or arguments; run commander-admin sessions --help")
	if configuration.dryRun {
		return invalid
	}
	filter := adminsessions.Filter{ScopeID: configuration.scopeID, OwnerAgentID: configuration.ownerID}
	if args[0] == "list" {
		if len(args) != 1 || configuration.requestID != "" {
			return invalid
		}
		return filter.Validate()
	}
	if len(args) != 2 || filter.ScopeID != "" || filter.OwnerAgentID != "" {
		return invalid
	}
	if !id.Valid(args[1]) {
		return errors.New("session ID must be a canonical UUID")
	}
	switch args[0] {
	case "inspect":
		if configuration.requestID != "" {
			return invalid
		}
	case "verify", "close", "abandon":
		if configuration.requestID != "" && !id.Valid(configuration.requestID) {
			return errors.New("request ID must be a canonical UUID")
		}
	default:
		return invalid
	}
	return nil
}

func sessionOwner(item adminsessions.Session) string {
	if item.OwnerAgentID == "" {
		return "unassigned"
	}
	return item.OwnerAgentID
}

func showSession(output io.Writer, item adminsessions.Session) error {
	if _, err := fmt.Fprintf(output, "Session: %s\nScope: %s\nOwner: %s\nState: %s\nLifecycle: %s\n", item.ID, item.ScopeID, sessionOwner(item), item.State, item.Disposition); err != nil {
		return err
	}
	for _, field := range []struct{ name, value string }{
		{"Label", item.Label}, {"Release", item.ReleaseReason}, {"Reason", item.Reason}, {"Endpoint", item.Endpoint},
	} {
		if field.value != "" {
			if _, err := fmt.Fprintf(output, "%s: %q\n", field.name, field.value); err != nil {
				return err
			}
		}
	}
	if item.ReleaseReason == "abandoned" {
		_, err := fmt.Fprintln(output, "Resource closure is unconfirmed.")
		return err
	}
	return nil
}

func sessionHelp(output io.Writer) error {
	_, err := io.WriteString(output, `Usage: commander-admin sessions <command> [options]

Commands:
  list                         List current and retained sessions
  inspect SESSION_ID           Read saved ownership and lifecycle
  verify SESSION_ID            Verify the resource through its owner
  close SESSION_ID             Request closure; inspect the returned state
  abandon SESSION_ID           Release a lost resource without confirming closure

List filters: --scope-id UUID, --owner-agent-id ID. Filters intersect.
Control option: --request-id UUID sets the submitted control request identity.
After an uncertain control, inspect saved state before submitting again.
Options: --json, --api-url URL, --api-username NAME, -h, --help.
`)
	return err
}
