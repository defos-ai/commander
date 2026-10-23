package id

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
)

type Role string

const (
	Agent      Role = "agent"
	Controller Role = "controller"
	Adapter    Role = "adapter"
)

const maxRuntimeIDLength = 63

// NewRuntime returns <type>-<role>-<8 lowercase hex characters>. The type must
// contain lowercase letters, digits, or single hyphens between nonempty parts.
// The complete ID fits within a DNS label and can be used as a path component.
func NewRuntime(runtimeType string, role Role) (string, error) {
	if !validRole(role) {
		return "", fmt.Errorf("unknown runtime role %q", role)
	}
	if !ValidType(runtimeType) {
		return "", fmt.Errorf("invalid runtime type %q: use lowercase letters, digits, and single hyphens between nonempty parts", runtimeType)
	}
	if len(runtimeType)+len(role)+10 > maxRuntimeIDLength {
		return "", fmt.Errorf("runtime type %q is too long: %s IDs must be at most %d characters", runtimeType, role, maxRuntimeIDLength)
	}

	var suffix [4]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", fmt.Errorf("generate runtime ID: %w", err)
	}
	return runtimeType + "-" + string(role) + "-" + hex.EncodeToString(suffix[:]), nil
}

func ValidRuntime(value string, role Role) bool {
	_, ok := RuntimeType(value, role)
	return ok
}

func RuntimeType(value string, role Role) (string, bool) {
	if !validRole(role) || len(value) > maxRuntimeIDLength || len(value) < len(role)+11 {
		return "", false
	}

	separator := len(value) - 9
	if value[separator] != '-' {
		return "", false
	}
	if !validRuntimeSuffix(value[separator+1:]) {
		return "", false
	}
	typedRole := value[:separator]
	runtimeType, ok := strings.CutSuffix(typedRole, "-"+string(role))
	if !ok || !ValidType(runtimeType) {
		return "", false
	}
	return runtimeType, true
}

func validRuntimeSuffix(value string) bool {
	if len(value) != 8 {
		return false
	}
	for _, c := range value {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func validRole(role Role) bool {
	return role == Agent || role == Controller || role == Adapter
}

// ValidType reports whether value is a lowercase runtime type suitable for a
// DNS label, routing component, or path component. NewRuntime additionally
// limits the type length to leave room for the role and random suffix.
func ValidType(value string) bool {
	if value == "" || len(value) > maxRuntimeIDLength || value[0] == '-' || value[len(value)-1] == '-' {
		return false
	}
	for i, c := range value {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case c == '-' && value[i-1] != '-':
		default:
			return false
		}
	}
	return true
}

func ValidAgent(value string) bool {
	return ValidRuntime(value, Agent)
}

func ValidAdapter(value string) bool {
	return ValidRuntime(value, Adapter)
}
