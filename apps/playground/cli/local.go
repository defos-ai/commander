package cli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/defos-ai/commander/apps/playground/internal/local"
	"github.com/defos-ai/commander/apps/playground/internal/ui"
	"github.com/defos-ai/commander/framework/host"
)

func (c *config) attachLocal(ctx context.Context) error {
	if !c.canAttachLocal() {
		return nil
	}
	directory := filepath.Join(c.StateDir, "local")
	instance, err := local.Discover(ctx, directory)
	if errors.Is(err, local.ErrNotRunning) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("discover local Commander: %w", err)
	}
	api, mcp, err := local.SavedCredentials(directory)
	if err != nil {
		return fmt.Errorf("read local Commander credentials: %w", err)
	}
	c.MCP = ui.Connection[host.BearerCredentials]{URL: instance.MCPURL, Credentials: mcp}
	c.API = ui.Connection[host.BasicCredentials]{URL: instance.APIURL, Credentials: api}
	c.localConnection, c.localMCPURL = true, instance.MCPURL
	c.localAdminURL = instance.AdminURL
	c.AdminURL = ""
	c.AdminHint = "Start Admin with /admin serve."
	if instance.AdminURL != "" {
		c.Config.SetAdminURL(instance.AdminURL)
	}
	return nil
}

func (c *config) canAttachLocal() bool {
	if c.AllInOne || c.Temporary || c.savedRemote {
		return false
	}
	for _, option := range []struct{ flag, environment string }{
		{"mcp-url", "COMMANDER_MCP_URL"}, {"api-url", "COMMANDER_API_URL"}, {"admin-url", "COMMANDER_ADMIN_URL"},
		{"", "COMMANDER_MCP_TOKEN"}, {"", "COMMANDER_API_USERNAME"}, {"", "COMMANDER_API_PASSWORD"},
	} {
		if c.settingProvided(option.flag, option.environment) {
			return false
		}
	}
	return true
}

func (c *config) localScope(endpoint string) bool {
	return c.AllInOne || c.localConnection && endpoint == c.localMCPURL
}

func (c *config) configureClientAdmin(start func(context.Context, []string) (string, error)) {
	if c.localAdminURL == "" {
		c.configureAdminServer(start)
		return
	}
	address := c.localAdminURL
	c.configureAdminServer(func(ctx context.Context, args []string) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if len(args) != 0 {
			return "", errors.New("Admin is owned by the local Commander instance; configure it when starting that instance")
		}
		return address, nil
	})
}
