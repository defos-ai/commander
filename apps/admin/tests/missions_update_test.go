package tests

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/defos-ai/commander/apps/admin/cli"
	"github.com/defos-ai/commander/apps/admin/client"
	adminmissions "github.com/defos-ai/commander/apps/admin/missions"
	"github.com/defos-ai/commander/apps/admin/tests/fakes"
	"github.com/defos-ai/commander/apps/admin/tests/fixtures"
)

func TestMissionUpdateReplacesOnlyNamedDefinition(t *testing.T) {
	name := "-review/site%new\\draft"
	original := fixtures.MissionJSON(name, "Keep the original until update")
	other := fixtures.MissionJSON("other", "Leave this mission unchanged")
	fixture := fixtures.NewMissionAPI(t, fakes.MissionAPIConfig{Records: map[string]json.RawMessage{
		name: json.RawMessage(original), "other": json.RawMessage(other),
	}})
	updated := fixtures.MissionJSON(name, "Review the revised objectives")
	updated = strings.Replace(updated, `"required_actions":["review"]`, `"required_actions":["inspect","summarize"],"optional_actions":["publish"]`, 1)
	updated = strings.Replace(updated, `"starting_actions":["review"]`, `"starting_actions":["inspect"]`, 1)
	updated = strings.Replace(updated, `"output_schema":{}`, `"output_schema":{"type":"object","properties":{"report":{"type":"string"}}}`, 1)

	output, err := missionCommand(t, fixture.Connection(), strings.NewReader(updated), "--json", "missions", "update", "-")

	if err != nil {
		t.Fatal(err)
	}
	assertMissionJSON(t, output, name)
	state := fixture.Snapshot()
	if len(state.Records) != 2 || string(state.Records[name]) != updated || string(state.Records["other"]) != other ||
		!reflect.DeepEqual(state.Requests, []string{"PUT " + name}) {
		t.Fatalf("update changed another mission, rewrote JSON, or sent extra requests: %v", state.Requests)
	}
	var saved, want map[string]json.RawMessage
	if json.Unmarshal(output, &saved) != nil || json.Unmarshal([]byte(updated), &want) != nil {
		t.Fatal("update did not return a mission definition")
	}
	for _, field := range []string{"objectives", "required_actions", "optional_actions", "starting_actions", "output_schema"} {
		var actualValue, expectedValue any
		if json.Unmarshal(saved[field], &actualValue) != nil || json.Unmarshal(want[field], &expectedValue) != nil || !reflect.DeepEqual(actualValue, expectedValue) {
			t.Fatalf("update response lost %s", field)
		}
	}
}

func TestMissionUpdateMissingTargetDoesNotCreate(t *testing.T) {
	fixture := fixtures.NewMissionAPI(t, fakes.MissionAPIConfig{})
	definition := fixtures.MissionJSON("missing", "Do not create this mission")

	output, err := missionCommand(t, fixture.Connection(), strings.NewReader(definition), "missions", "update", "-")

	var response *client.StatusError
	if !errors.As(err, &response) || response.StatusCode != http.StatusNotFound || response.Code != "mission_not_found" || len(output) != 0 {
		t.Fatal("missing update target did not return its typed API failure")
	}
	state := fixture.Snapshot()
	if len(state.Records) != 0 || !reflect.DeepEqual(state.Requests, []string{"PUT missing"}) {
		t.Fatal("mission update created a missing definition or retried")
	}
}

func TestMissionUpdateUncertainResponseIsNotRetried(t *testing.T) {
	for _, scenario := range []struct {
		name string
		api  fakes.MissionAPIConfig
	}{
		{name: "lost acknowledgement", api: fakes.MissionAPIConfig{DropAcknowledgement: "saved"}},
		{name: "unexpected success status", api: fakes.MissionAPIConfig{UpdateStatus: http.StatusCreated}},
		{name: "wrong response name", api: fakes.MissionAPIConfig{UpdateResponse: json.RawMessage(fixtures.MissionJSON("other", "Wrong mission"))}},
		{name: "invalid response", api: fakes.MissionAPIConfig{UpdateResponse: json.RawMessage(`[]`)}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			scenario.api.Records = map[string]json.RawMessage{"saved": json.RawMessage(fixtures.MissionJSON("saved", "Old objective"))}
			fixture := fixtures.NewMissionAPI(t, scenario.api)
			updated := fixtures.MissionJSON("saved", "Saved before acknowledgement failed")

			output, err := missionCommand(t, fixture.Connection(), strings.NewReader(updated), "missions", "update", "-", "--json")

			if !errors.Is(err, client.ErrWriteUncertain) || len(output) != 0 {
				t.Fatal("unconfirmed mission update did not preserve uncertainty")
			}
			state := fixture.Snapshot()
			if string(state.Records["saved"]) != updated || !reflect.DeepEqual(state.Requests, []string{"PUT saved"}) {
				t.Fatal("uncertain mission update was retried or not applied by the fixture")
			}
		})
	}
}

func TestMissionUpdateInputValidationPrecedesAPISetup(t *testing.T) {
	valid := fixtures.MissionJSON("saved", "Valid objective")
	for _, scenario := range []struct {
		name, body, contains string
		options              []string
	}{
		{name: "multiple definitions", body: "[" + valid + "," + fixtures.MissionJSON("second", "Another objective") + "]", contains: "exactly one"},
		{name: "invalid mission", body: strings.Replace(valid, `"objectives":["Valid objective"]`, `"objectives":[]`, 1), contains: "objectives"},
		{name: "oversized definition", body: fixtures.MissionJSON("saved", strings.Repeat("x", 1<<20)), contains: "1 MiB"},
		{name: "dry run", body: valid, options: []string{"--dry-run"}, contains: "invalid mission command"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			connection := client.Connection{URL: ":invalid"}
			args := append([]string{"missions", "update", "-"}, scenario.options...)

			output, err := missionCommand(t, connection, strings.NewReader(scenario.body), args...)

			var usage *cli.UsageError
			if !errors.As(err, &usage) || !strings.Contains(err.Error(), scenario.contains) || len(output) != 0 {
				t.Fatalf("invalid update reached API setup or produced success output: %v", err)
			}
		})
	}
}

func TestMissionUpdateRejectsMismatchedDefinitionIdentity(t *testing.T) {
	definition := adminmissions.Definition{Name: "original", JSON: json.RawMessage(fixtures.MissionJSON("different", "Changed name"))}

	_, err := adminmissions.Update(t.Context(), nil, definition)

	if err == nil || !strings.Contains(err.Error(), "name does not match") {
		t.Fatal("mismatched mission identity reached API transport")
	}
}
