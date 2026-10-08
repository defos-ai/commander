package ui

import "fmt"

func (m *Model) localCommand(value string) bool {
	switch value {
	case "/credentials":
		m.input.Reset()
		if api := m.config.LocalAPI; api != nil {
			m.add("notice", fmt.Sprintf("API: %s\nUsername: %s\nPassword: %s", api.URL, api.Username, api.Password))
		} else {
			m.add("notice", "Local API credentials are available only with --all-in-one.")
		}
		return true
	case "/help":
		if m.config.LocalAPI != nil {
			m.add("notice", "/credentials shows local API credentials in this terminal only.")
		}
	}
	return false
}
