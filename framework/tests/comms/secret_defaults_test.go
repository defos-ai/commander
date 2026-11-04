package comms_test

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/comms"
)

func TestReusableSecretDefaultPreservesOwnedBytesAndRequestBinding(t *testing.T) {
	request := fixtures.ReusableSecretRequest()
	value := []byte("private-default-wire-fixture")
	message, err := controllerevents.NewSecretSyncRequested(request, value)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(message.Payload)
	clear(value)

	decoded, err := controllerevents.DecodeSecretSyncRequested(message)
	if err != nil || string(decoded.Default) != "private-default-wire-fixture" || decoded.Request.ID != request.ID {
		t.Fatal("default did not retain independent bytes and request identity")
	}
	for _, formatted := range []string{fmt.Sprint(decoded), fmt.Sprintf("%+v", decoded), fmt.Sprintf("%#v", decoded)} {
		if strings.Contains(formatted, string(decoded.Default)) || strings.Contains(formatted, base64.StdEncoding.EncodeToString(decoded.Default)) {
			t.Fatal("request formatting exposed default bytes")
		}
	}
	clear(decoded.Default)
	message.AgentID = "other-agent-01234567"

	if _, err := controllerevents.DecodeSecretSyncRequested(message); err == nil {
		t.Fatal("default-bearing request crossed its original recipient binding")
	}
}

func TestSecretDefaultBoundsPreserveEmptyValuesAndExcludeTransientRequests(t *testing.T) {
	request := fixtures.ReusableSecretRequest()
	for _, value := range [][]byte{nil, {}, bytes.Repeat([]byte{'x'}, controllerevents.MaxSecretDefaultBytes)} {
		message, err := controllerevents.NewSecretSyncRequested(request, value)
		if err != nil {
			t.Fatal("valid reusable default was rejected")
		}
		decoded, err := controllerevents.DecodeSecretSyncRequested(message)
		clear(message.Payload)
		if err != nil || (decoded.Default == nil) != (value == nil) || !bytes.Equal(decoded.Default, value) {
			t.Fatal("default transport lost absence, explicit emptiness or bytes")
		}
		clear(decoded.Default)
	}

	if _, err := controllerevents.NewSecretSyncRequested(request, make([]byte, controllerevents.MaxSecretDefaultBytes+1)); err == nil {
		t.Fatal("oversized default entered the secret exchange")
	}
	if _, err := controllerevents.NewSecretSyncRequested(fixtures.TransientSecretRequest(), []byte{}); err == nil {
		t.Fatal("transient request accepted an explicitly empty default")
	}
}
