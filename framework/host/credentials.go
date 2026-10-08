package host

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"unicode"
)

var ErrMissingCredentials = errors.New("username and password must both be set and not blank")

type Credentials struct {
	Username string
	Password string
}

func (c Credentials) Validate() error {
	if strings.TrimSpace(c.Username) == "" || strings.TrimSpace(c.Password) == "" {
		return ErrMissingCredentials
	}
	if strings.Contains(c.Username, ":") || strings.ContainsFunc(c.Username, unicode.IsControl) || strings.ContainsFunc(c.Password, unicode.IsControl) {
		return errors.New("credentials must not contain control characters; username must not contain ':'")
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
