package ui

import (
	"fmt"
	"strings"
)

func (m *Model) localCommand(value string) bool {
	if value == "/help" && m.config.LocalAPI != nil {
		m.add("notice", "/credentials api shows the API login; /credentials mcp shows the MCP token. /credentials shows both in this terminal only.")
	}
	args := strings.Fields(value)
	if len(args) == 0 || args[0] != "/credentials" {
		return false
	}
	m.input.Reset()
	target := ""
	if len(args) == 2 {
		target = args[1]
	}
	if len(args) > 2 || (target != "" && target != "api" && target != "mcp") {
		m.add("notice", "Usage: /credentials [api|mcp]")
		return true
	}
	api := m.config.LocalAPI
	if api == nil {
		m.add("notice", "Local credentials are available only with --all-in-one.")
		return true
	}
	if target != "mcp" {
		m.add("notice", fmt.Sprintf("API: %s\nUsername: %s\nPassword: %s", api.URL, api.Username, api.Password))
	}
	if target != "api" {
		m.add("notice", fmt.Sprintf("MCP: %s\nToken: %s", m.config.MCPURL, m.config.MCPCredentials.Token))
	}
	return true
}
