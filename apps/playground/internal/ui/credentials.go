package ui

import (
	"fmt"
	"runtime"
	"strings"
)

func tokenEnvironmentCommand(command string) string {
	if strings.HasPrefix(command, "/") {
		return ""
	}
	command = strings.Replace(command, "docker compose exec mcp ", "docker compose exec -T mcp ", 1)
	if runtime.GOOS == "windows" {
		return fmt.Sprintf("$env:COMMANDER_MCP_TOKEN = (%s --raw)", command)
	}
	return fmt.Sprintf("export COMMANDER_MCP_TOKEN=\"$(%s --raw)\"", command)
}

func (m *Model) localCommand(value string) bool {
	if value == "/status" && m.stage == stageChat {
		m.add("notice", "MCP: "+m.config.MCPURL)
		if m.config.LocalAPI != nil {
			m.add("notice", "API: "+m.config.LocalAPI.URL)
		}
		if m.config.AdminURL != "" {
			m.add("notice", "Admin: "+m.config.AdminURL)
		}
		return false
	}
	args := strings.Fields(value)
	if len(args) == 0 || args[0] != "/credentials" {
		return false
	}
	m.input.Reset()
	if len(args) != 1 {
		m.add("notice", "Usage: /credentials")
		return true
	}
	api := m.config.LocalAPI
	if api == nil {
		m.add("notice", "Local credentials are available only with --all-in-one.")
		return true
	}
	m.add("notice", fmt.Sprintf("API: %s\nUsername: %s\nPassword: %s\n\nMCP: %s\nToken: %s", api.URL, api.Username, api.Password, m.config.MCPURL, m.config.MCPCredentials.Token))
	return true
}
