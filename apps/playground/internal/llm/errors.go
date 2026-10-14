package llm

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode"
)

// AuthenticationError identifies a rejected credential or access permission.
// Its diagnostic is redacted before wrapping; it does not imply a safe retry.
type AuthenticationError struct {
	Err error
}

func (e *AuthenticationError) Error() string { return e.Err.Error() }
func (e *AuthenticationError) Unwrap() error { return e.Err }

func (c *Client) authenticationFailure(status int, body []byte) bool {
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return true
	}
	if c.config.Provider != "gemini" || status != http.StatusBadRequest {
		return false
	}
	// Gemini reports invalid keys as structured HTTP 400 errors, alongside
	// unrelated bad requests that must retain the normal endpoint/model flow.
	var response struct {
		Error struct {
			Details []struct {
				Type   string `json:"@type"`
				Reason string `json:"reason"`
			} `json:"details"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &response) != nil {
		return false
	}
	for _, detail := range response.Error.Details {
		if detail.Type == "type.googleapis.com/google.rpc.ErrorInfo" && detail.Reason == "API_KEY_INVALID" {
			return true
		}
	}
	return false
}

func (c *Client) diagnosticError(summary string, body []byte) error {
	if !c.config.Debug {
		return errors.New(summary + "; rerun with --debug for details")
	}
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	if decoder.Decode(&value) == nil && json.Valid(body) {
		body, _ = json.Marshal(c.redactValue(value))
	}
	detail := c.redactKey(string(body))
	// Provider text is untrusted terminal input. Do not render control sequences.
	detail = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return ' '
		}
		return r
	}, detail)
	detail = strings.TrimSpace(detail)
	if detail == "" {
		detail = "provider returned no error details"
	}
	// Bound the displayed text only after redaction, so truncation cannot leak
	// the beginning of an API key that crosses the display boundary.
	if text := []rune(detail); len(text) > 4096 {
		detail = string(text[:4096]) + "… [truncated]"
	}
	return errors.New(summary + "\nDetails: " + detail)
}

func (c *Client) redactValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		for key, item := range v {
			name := strings.NewReplacer("-", "", "_", "").Replace(strings.ToLower(key))
			switch name {
			case "authorization", "apikey", "xapikey", "password", "token", "accesstoken", "refreshtoken":
				v[key] = "[REDACTED]"
			default:
				v[key] = c.redactValue(item)
			}
		}
	case []any:
		for i := range v {
			v[i] = c.redactValue(v[i])
		}
	case string:
		return c.redactKey(v)
	}
	return value
}

func (c *Client) redactKey(text string) string {
	if key := c.config.APIKey; key != "" {
		encoded, _ := json.Marshal(key)
		text = strings.ReplaceAll(text, string(encoded[1:len(encoded)-1]), "[REDACTED]")
		text = strings.ReplaceAll(text, key, "[REDACTED]")
	}
	return text
}
