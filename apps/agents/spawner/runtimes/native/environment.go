package native

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

func copyEnvironment(input map[string]string) (map[string]string, error) {
	result := make(map[string]string, len(input))
	for key, value := range input {
		switch key {
		case "PATH", "SystemRoot", "WINDIR", "TMPDIR", "TMP", "TEMP",
			"CHROMIUM_PATH", "CHROMIUM_NO_SANDBOX",
			"AWS_ENDPOINT_URL_S3", "AWS_REGION", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "S3_USE_PATH_STYLE":
		default:
			return nil, fmt.Errorf("unsupported child environment setting %q", key)
		}
		if strings.ContainsRune(value, 0) {
			return nil, errors.New("child environment setting contains a NUL byte")
		}
		result[key] = value
	}
	return result, nil
}

func (m *Manager) environment(agentID, directory string) []string {
	// The manager generated and saved this full ID; the child supplies its own type.
	suffix := agentID[strings.LastIndexByte(agentID, '-')+1:]
	values := []string{
		"COMMANDER_AGENT_ID_SUFFIX=" + suffix,
		"BUS_URL=" + m.config.BusURL,
		"STORAGE_URL=" + m.config.StorageURL,
		"HOME=" + directory,
		"USERPROFILE=" + directory,
	}
	for key, value := range m.config.Environment {
		values = append(values, key+"="+value)
	}
	sort.Strings(values)
	return values
}
