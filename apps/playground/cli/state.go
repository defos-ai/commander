package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/defos-ai/commander/apps/admin/client"
	"github.com/defos-ai/commander/apps/playground/internal/llm"
	"github.com/defos-ai/commander/apps/playground/internal/state"
	"github.com/defos-ai/commander/apps/playground/internal/ui"
)

func stateDirectory(configured string) (string, error) {
	if configured != "" {
		return filepath.Abs(configured)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", errors.New("cannot find your home directory; set --state-dir or run with --temporary")
	}
	return filepath.Join(home, ".commander"), nil
}

func (c *config) openState() (*state.Profile, error) {
	if c.Temporary {
		return nil, nil
	}
	directory, err := stateDirectory(c.StateDir)
	if err != nil {
		return nil, err
	}
	profile, err := state.Open(directory)
	if err != nil {
		return nil, fmt.Errorf("open Playground state: %w; choose another --state-dir or run with --temporary", err)
	}
	saved, err := profile.Load()
	if err == nil {
		err = c.applySettings(saved)
	}
	if err != nil {
		return nil, errors.Join(err, profile.Close())
	}
	c.StateDir = profile.Directory
	c.SaveSettings = func(settings state.Settings) error {
		if c.AllInOne {
			settings.Remote = saved.Remote
		}
		return profile.Save(settings)
	}
	c.LoadProviderKey = profile.LoadKey
	c.SaveProviderKey = profile.SaveKey
	scope := func(endpoint string) string {
		if c.AllInOne {
			return "local"
		}
		return "remote:" + endpoint
	}
	c.LoadHistory = func(endpoint string) (ui.History, error) {
		data, err := profile.LoadHistory(scope(endpoint))
		var history ui.History
		if err == nil && len(data) != 0 {
			decoder := json.NewDecoder(bytes.NewReader(data))
			decoder.DisallowUnknownFields()
			if decoder.Decode(&history) != nil || history.Version != 1 {
				err = errors.New("saved conversation format is invalid or incompatible")
			}
		}
		return history, err
	}
	c.SaveHistory = func(endpoint string, history ui.History) error {
		data, err := json.Marshal(history)
		if err != nil {
			return errors.New("could not encode Playground history")
		}
		return profile.SaveHistory(scope(endpoint), data)
	}
	return profile, nil
}

func (c *config) settingProvided(flag, environment string) bool {
	return c.provided[flag] || strings.TrimSpace(os.Getenv(environment)) != ""
}

func (c *config) applySettings(saved state.Settings) error {
	if !c.settingProvided("provider", "COMMANDER_LLM_PROVIDER") {
		c.Provider = saved.Provider
	}
	known := c.Provider == ""
	for _, preset := range llm.Presets() {
		known = known || preset.ID == c.Provider
	}
	if !known {
		return errors.New("saved provider is unknown; choose a provider with --provider")
	}
	if strings.EqualFold(c.Provider, saved.Provider) {
		if !c.settingProvided("model", "COMMANDER_LLM_MODEL") {
			c.Model = saved.Model
		}
		if !c.settingProvided("base-url", "COMMANDER_LLM_BASE_URL") {
			c.BaseURL = saved.BaseURL
		}
	}
	if c.BaseURL != "" {
		if err := llm.ValidateURL(c.BaseURL); err != nil {
			return errors.New("saved model API URL is invalid; choose another --state-dir or run with --temporary")
		}
	}
	if c.AllInOne {
		return nil
	}
	if !c.settingProvided("mcp-url", "COMMANDER_MCP_URL") && saved.Remote.MCPURL != "" {
		c.MCP.URL = saved.Remote.MCPURL
	}
	if !c.settingProvided("api-url", "COMMANDER_API_URL") {
		c.API.URL = saved.Remote.APIURL
	}
	if c.API.URL == saved.Remote.APIURL && !c.settingProvided("", "COMMANDER_API_USERNAME") && saved.Remote.APIUsername != "" {
		c.API.Credentials.Username = saved.Remote.APIUsername
	}
	if err := llm.ValidateURL(c.MCP.URL); err != nil {
		return errors.New("saved MCP URL is invalid; use --mcp-url to select an endpoint")
	}
	if c.API.URL != "" {
		if err := client.ValidateURL(c.API.URL); err != nil {
			return errors.New("saved API URL is invalid; use --api-url to select an endpoint")
		}
	}
	return nil
}
