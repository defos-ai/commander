package sessions

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

func fillSessionInput(input []byte, scopeID, sessionID string) ([]byte, error) {
	body := bytes.TrimSpace(input)
	if len(body) == 0 {
		body = []byte("{}")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return nil, errors.New("session_policy_violation: action input must be a JSON object")
	}
	expected := map[string]string{"session_scope": scopeID, "session_id": sessionID}
	seen := make(map[string]bool)
	fields := 0
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := token.(string)
		if !ok {
			return nil, errors.New("session_policy_violation: invalid action input key")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		fields++
		for name, target := range expected {
			if !strings.EqualFold(key, name) {
				continue
			}
			if key != name || seen[name] {
				return nil, fmt.Errorf("session_policy_violation: %s must appear once with its canonical name", name)
			}
			var actual string
			if target == "" || json.Unmarshal(value, &actual) != nil || actual != target {
				return nil, fmt.Errorf("session_policy_violation: conflicting %s", name)
			}
			seen[name] = true
		}
	}
	if closing, err := decoder.Token(); err != nil || closing != json.Delim('}') {
		return nil, errors.New("session_policy_violation: invalid action input object")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, errors.New("session_policy_violation: expected a single action input object")
	}
	var additions []string
	for _, name := range []string{"session_scope", "session_id"} {
		if expected[name] != "" && !seen[name] {
			additions = append(additions, strconv.Quote(name)+":"+strconv.Quote(expected[name]))
		}
	}
	if len(additions) == 0 {
		return input, nil
	}
	// Append only routing fields; business JSON retains its original tokens and escapes.
	result := append([]byte(nil), body[:len(body)-1]...)
	if fields != 0 {
		result = append(result, ',')
	}
	result = append(result, strings.Join(additions, ",")...)
	return append(result, '}'), nil
}
