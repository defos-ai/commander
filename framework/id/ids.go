// Package id provides UUIDs for messages and resources and typed runtime IDs.
package id

import (
	"crypto/rand"
	"encoding/hex"
)

// AgentID returns <type>-agent-<8 lowercase hex characters>.
func AgentID(agentType string) (string, error) { return NewRuntime(agentType, Agent) }

// ControllerID returns <type>-controller-<8 lowercase hex characters>.
func ControllerID(controllerType string) (string, error) {
	return NewRuntime(controllerType, Controller)
}

// AdapterID returns <type>-adapter-<8 lowercase hex characters>.
func AdapterID(adapterType string) (string, error) { return NewRuntime(adapterType, Adapter) }

func MessageID() string { return New() }

func OperationID() string { return New() }

func TaskID() string { return New() }

func QuestionID() string { return New() }

func ReportID() string { return New() }

func ArtifactID() string { return New() }

func ArtifactFileID() string { return New() }

// New returns a random UUIDv4. Failure of the OS random source is fatal.
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

// Valid accepts the canonical lowercase form emitted by New.
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
