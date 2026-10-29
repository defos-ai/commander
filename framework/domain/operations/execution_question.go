package operations

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	DefaultQuestionTimeout          = 2 * time.Minute
	MaxExecutionQuestionAnswerBytes = 8192
)

func ValidateExecutionQuestion(key, text string, options []string) error {
	if len(key) == 0 || len(key) > 64 || key[0] < 'a' || key[0] > 'z' {
		return errors.New("question key must start with a lowercase letter and contain at most 64 characters")
	}
	for _, c := range key {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' && c != '_' {
			return errors.New("question key may contain only lowercase letters, digits, hyphens and underscores")
		}
	}
	if len(text) > 8192 || !utf8.ValidString(text) || strings.TrimSpace(text) == "" {
		return errors.New("question text must be nonblank UTF-8 of at most 8192 bytes")
	}
	if len(options) > 4 {
		return errors.New("questions support at most four options")
	}
	for _, option := range options {
		if len(option) > 1024 || !utf8.ValidString(option) || strings.TrimSpace(option) == "" {
			return errors.New("question options must be nonblank UTF-8 of at most 1024 bytes")
		}
	}
	return nil
}
