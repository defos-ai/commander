package id

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

func AgentID(agentType string) (string, error) {
	return NewRuntime(strings.TrimSuffix(agentType, "-agent"), Agent)
}

// AgentIDFromSuffix combines the host's fixed type with an externally assigned
// suffix. Only eight lowercase hexadecimal characters may come from outside.
func AgentIDFromSuffix(agentType, suffix string) (string, error) {
	if !validRuntimeSuffix(suffix) {
		return "", errors.New("agent ID suffix must contain exactly 8 lowercase hex characters")
	}
	value := strings.TrimSuffix(agentType, "-agent") + "-agent-" + suffix
	if !ValidAgent(value) {
		return "", fmt.Errorf("invalid agent type %q for a runtime ID", agentType)
	}
	return value, nil
}

func ControllerID(controllerType string) (string, error) {
	return NewRuntime(controllerType, Controller)
}

func AdapterID(adapterType string) (string, error) { return NewRuntime(adapterType, Adapter) }

func MessageID() string { return New() }

func OperationID() string { return New() }

func TaskID() string { return New() }

func QuestionID() string { return New() }

func ReportID() string { return New() }

func ArtifactID() string { return New() }

func ArtifactFileID() string { return New() }

func New() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b[:])
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

func Valid(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i, c := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
