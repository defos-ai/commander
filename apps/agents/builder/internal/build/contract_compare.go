package build

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strings"

	"github.com/defos-ai/commander/framework/domain/actions"
)

// CompareActions checks the actual registration against the requested contracts.
// Only validated requested names may appear in errors; generated metadata is untrusted.
func CompareActions(expected, actual []actions.Definition) error {
	if err := ValidateActions(expected); err != nil {
		return errors.New("requested action contracts are invalid")
	}
	if err := ValidateActions(actual); err != nil {
		return errors.New("generated action contracts are invalid")
	}
	if len(expected) != len(actual) {
		return errors.New("generated action names do not match requested action contracts")
	}
	registered := make(map[string]actions.Definition, len(actual))
	for _, definition := range actual {
		registered[definition.Name] = definition
	}
	for _, requested := range expected {
		generated, exists := registered[requested.Name]
		if !exists {
			return errors.New("generated action names do not match requested action contracts")
		}
		if requested.SessionMode != generated.SessionMode {
			return fmt.Errorf("generated action %q does not match its requested session mode", requested.Name)
		}
		if requested.Description != generated.Description {
			return fmt.Errorf("generated action %q does not match its requested description", requested.Name)
		}
		if !sameSecretNames(requested.RequiredSecrets, generated.RequiredSecrets) {
			return fmt.Errorf("generated action %q does not match its requested required secrets", requested.Name)
		}
		for _, field := range []struct {
			name             string
			expected, actual json.RawMessage
		}{
			{"input_schema", requested.InputSchema, generated.InputSchema},
			{"output_schema", requested.OutputSchema, generated.OutputSchema},
		} {
			if !sameSchema(field.expected, field.actual) {
				return fmt.Errorf("generated action %q does not match its requested %s", requested.Name, field.name)
			}
		}
	}
	return nil
}

func sameSecretNames(expected, actual []string) bool {
	if len(expected) != len(actual) {
		return false
	}
	names := make(map[string]bool, len(expected))
	for _, name := range expected {
		names[name] = true
	}
	for _, name := range actual {
		if !names[name] {
			return false
		}
	}
	return true
}

func sameSchema(expected, actual json.RawMessage) bool {
	decode := func(data json.RawMessage) (any, error) {
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		if decoder.Decode(new(any)) != io.EOF {
			return nil, errors.New("expected one JSON value")
		}
		return value, nil
	}
	left, err := decode(expected)
	if err != nil {
		return false
	}
	right, err := decode(actual)
	return err == nil && sameJSONValue(left, right)
}

func sameJSONValue(expected, actual any) bool {
	switch left := expected.(type) {
	case map[string]any:
		right, ok := actual.(map[string]any)
		if !ok || len(left) != len(right) {
			return false
		}
		for name, value := range left {
			other, exists := right[name]
			if !exists || !sameJSONValue(value, other) {
				return false
			}
		}
		return true
	case []any:
		right, ok := actual.([]any)
		if !ok || len(left) != len(right) {
			return false
		}
		for index, value := range left {
			if !sameJSONValue(value, right[index]) {
				return false
			}
		}
		return true
	case json.Number:
		right, ok := actual.(json.Number)
		return ok && normalizedNumber(left) == normalizedNumber(right)
	default:
		return expected == actual
	}
}

// Keep decimal digits and an arbitrary-size exponent separately. Expanding a
// value such as 1e1000000000 into a rational would allocate according to its value.
func normalizedNumber(value json.Number) string {
	text := string(value)
	negative := strings.HasPrefix(text, "-")
	text = strings.TrimPrefix(text, "-")
	mantissa, exponentText := text, "0"
	if index := strings.IndexAny(text, "eE"); index >= 0 {
		mantissa, exponentText = text[:index], text[index+1:]
	}
	whole, fraction, _ := strings.Cut(mantissa, ".")
	digits := strings.TrimLeft(whole+fraction, "0")
	if digits == "" {
		return "0"
	}
	trimmed := strings.TrimRight(digits, "0")
	var exponent big.Int
	// JSON decoding has already validated the complete numeric spelling.
	exponent.SetString(exponentText, 10)
	exponent.Add(&exponent, big.NewInt(int64(len(digits)-len(trimmed)-len(fraction))))
	if negative {
		trimmed = "-" + trimmed
	}
	return trimmed + "e" + exponent.String()
}
