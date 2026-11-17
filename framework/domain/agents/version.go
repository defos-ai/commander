package agents

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

func ValidateVersion(version string) error {
	if version == "" {
		return nil
	}
	if len(version) > 128 || !utf8.ValidString(version) || strings.IndexFunc(version, func(value rune) bool {
		return unicode.IsSpace(value) || unicode.IsControl(value)
	}) >= 0 {
		return errors.New("agent version must be UTF-8 text of at most 128 bytes without whitespace or control characters")
	}
	return nil
}
