package host

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"unicode"
)

var ErrMissingCredentials = errors.New("credentials must be set and not blank")

type Credentials interface {
	Validate() error
}

type BasicCredentials struct {
	Username string
	Password string
}

func (c BasicCredentials) Validate() error {
	if strings.TrimSpace(c.Username) == "" || strings.TrimSpace(c.Password) == "" {
		return ErrMissingCredentials
	}
	if strings.Contains(c.Username, ":") || strings.ContainsFunc(c.Username, unicode.IsControl) || strings.ContainsFunc(c.Password, unicode.IsControl) {
		return errors.New("credentials must not contain control characters; username must not contain ':'")
	}
	return nil
}

type BearerCredentials struct {
	Token string
}

func (c BearerCredentials) Validate() error {
	if strings.TrimSpace(c.Token) == "" {
		return ErrMissingCredentials
	}
	token := strings.TrimRight(c.Token, "=")
	if token == "" || strings.ContainsFunc(token, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-._~+/", r))
	}) {
		return errors.New("bearer token contains invalid characters or padding")
	}
	return nil
}

func GeneratePassword() (string, error) {
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(secret[:]), nil
}
