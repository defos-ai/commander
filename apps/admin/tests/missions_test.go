package tests

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/defos-ai/commander/apps/admin/cli"
	"github.com/defos-ai/commander/apps/admin/client"
	adminmissions "github.com/defos-ai/commander/apps/admin/missions"
	"github.com/defos-ai/commander/apps/admin/tests/fakes"
	"github.com/defos-ai/commander/apps/admin/tests/fixtures"
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/host"
)

func TestMissionCommandsRoundTrip(t *testing.T) {
	fixture := fixtures.NewMissionAPI(t, fakes.MissionAPIConfig{})
	name := "-review/site%new\\draft"
	definition := fixtures.MissionJSON(name, "Review the requested site")
	path := fixtures.MissionsFile(t, definition)
	connection := fixture.Connection()
	connection.URL = ":invalid"
	output, err := missionCommand(t, connection, nil, "missions", "import", path, "--api-url", fixture.Connection().URL, "--json")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	result := importResult(t, output)
	if result.Counts.Created != 1 || len(result.Items) != 1 || result.Items[0].Status != "created" {
		t.Fatalf("unexpected import result: %s", output)
	}
	output, err = missionCommand(t, fixture.Connection(), nil, "--json", "missions", "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var list struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(output, &list); err != nil || len(list.Items) != 1 {
		t.Fatalf("invalid list output: %s (%v)", output, err)
	}
	assertMissionJSON(t, list.Items[0], name)
	output, err = missionCommand(t, fixture.Connection(), nil, "missions", "export", "--", name)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	assertMissionJSON(t, output, name)
}

func TestMissionInputValidationPrecedesAPIRequests(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("validation sent an API request")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	connection := client.Connection{URL: server.URL, Credentials: host.BasicCredentials{Username: "operator", Password: "private-password"}}
	valid := fixtures.MissionJSON("valid", "Review the site")
	for _, input := range []string{valid, "[" + valid + "]", `{"items":[` + valid + `]}`} {
		connection := client.Connection{URL: ":invalid"}
		output, err := missionCommand(t, connection, strings.NewReader(input), "missions", "validate", "-", "--json")
		var validation struct {
			Valid    bool     `json:"valid"`
			Missions []string `json:"missions"`
		}
		if err != nil || json.Unmarshal(output, &validation) != nil || !validation.Valid || !reflect.DeepEqual(validation.Missions, []string{"valid"}) {
			t.Fatalf("offline validation: output=%s error=%v", output, err)
		}
	}
	for _, test := range []struct{ name, input string }{
		{"invalid later definition", "[" + valid + `,{"name":"invalid","objectives":[],"input_schema":{},"output_schema":{}}]`},
		{"duplicate name", "[" + valid + "," + valid + "]"},
		{"oversized document", valid + strings.Repeat(" ", 4<<20)},
		{"oversized definition", fixtures.MissionJSON("large", strings.Repeat("x", 1<<20))},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := missionCommand(t, connection, strings.NewReader(test.input), "missions", "import", "-", "--json")
			if err == nil {
				t.Fatal("invalid import was accepted")
			}
		})
	}
}

func TestMissionDryRunPreservesExistingDefinitions(t *testing.T) {
	original := fixtures.MissionJSON("existing", "Keep this objective")
	fixture := fixtures.NewMissionAPI(t, fakes.MissionAPIConfig{Records: map[string]json.RawMessage{"existing": json.RawMessage(original)}})
	input := "[" + fixtures.MissionJSON("existing", "Do not replace the objective") + "," + fixtures.MissionJSON("new", "Create later") + "]"
	output, err := missionCommand(t, fixture.Connection(), strings.NewReader(input), "missions", "import", "-", "--dry-run", "--json")
	if err != nil {
		t.Fatal(err)
	}
	result := importResult(t, output)
	if result.Counts.Created != 0 || result.Counts.Skipped != 1 || result.Counts.WouldCreate != 1 || len(result.Items) != 2 || result.Items[0].Status != "skipped" || result.Items[1].Status != "would_create" {
		t.Fatalf("unexpected dry-run result: %s", output)
	}
	state := fixture.Snapshot()
	if len(state.Records) != 1 || string(state.Records["existing"]) != original || !reflect.DeepEqual(state.Requests, []string{"GET existing", "GET new"}) {
		t.Errorf("dry-run changed saved missions or submitted a write: %v", state.Requests)
	}
	output, err = missionCommand(t, fixture.Connection(), strings.NewReader(input), "missions", "import", "-", "--json")
	if err != nil {
		t.Fatal(err)
	}
	result = importResult(t, output)
	state = fixture.Snapshot()
	if result.Counts.Created != 1 || result.Counts.Skipped != 1 || len(state.Records) != 2 || string(state.Records["existing"]) != original || !reflect.DeepEqual(state.Requests, []string{"GET existing", "GET new", "GET existing", "GET new", "POST new"}) {
		t.Fatalf("import replaced an existing definition or failed to create the missing one: %s; requests=%v", output, state.Requests)
	}
}

func TestMissionImportStopsAfterUncertainWrite(t *testing.T) {
	fixture := fixtures.NewMissionAPI(t, fakes.MissionAPIConfig{DropAcknowledgement: "uncertain"})
	input := "[" + fixtures.MissionJSON("created", "Create first") + "," + fixtures.MissionJSON("uncertain", "Lose the acknowledgement") + "," + fixtures.MissionJSON("remaining", "Do not attempt") + "]"
	output, err := missionCommand(t, fixture.Connection(), strings.NewReader(input), "missions", "import", "-", "--json")
	if !errors.Is(err, client.ErrWriteUncertain) {
		t.Fatalf("lost acknowledgement did not preserve uncertain outcome: %v", err)
	}
	result := importResult(t, output)
	statuses := make([]string, len(result.Items))
	for i, item := range result.Items {
		statuses[i] = item.Status
	}
	if !reflect.DeepEqual(statuses, []string{"created", "uncertain", "not_attempted"}) || result.Counts.Created != 1 || result.Counts.Uncertain != 1 || result.Counts.NotAttempted != 1 {
		t.Fatalf("unexpected partial import: %s", output)
	}
	state := fixture.Snapshot()
	if len(state.Records) != 2 || !reflect.DeepEqual(state.Requests, []string{"GET created", "POST created", "GET uncertain", "POST uncertain"}) {
		t.Fatalf("uncertain write was retried or remaining work submitted: %v", state.Requests)
	}
}

func TestBundledMissionSeed(t *testing.T) {
	other := fixtures.MissionJSON("operator-mission", "Keep this mission")
	fixture := fixtures.NewMissionAPI(t, fakes.MissionAPIConfig{Records: map[string]json.RawMessage{"operator-mission": json.RawMessage(other)}})
	output, err := missionCommand(t, fixture.Connection(), nil, "missions", "seed", "--dry-run", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if result := importResult(t, output); result.Counts.WouldCreate != 3 || result.Counts.Created != 0 {
		t.Fatalf("unexpected seed dry run: %s", output)
	}
	state := fixture.Snapshot()
	if len(state.Records) != 1 {
		t.Error("seed dry run wrote mission definitions")
	}

	api, err := client.New(fixture.Connection())
	if err != nil {
		t.Fatal(err)
	}
	defer api.Close()
	result, err := adminmissions.Seed(t.Context(), api, false)
	if err != nil || result.Counts.Created != 3 {
		t.Fatalf("seed bundled definitions: %+v, error=%v", result, err)
	}
	definitions, err := adminmissions.List(t.Context(), api)
	if err != nil {
		t.Fatal(err)
	}
	catalog := make(map[string]missions.Mission)
	for _, definition := range definitions {
		mission, err := missions.LoadMission(definition.JSON)
		if err != nil {
			t.Fatal(err)
		}
		catalog[mission.Name] = mission
	}
	t.Run("website checks", func(t *testing.T) { checkWebsiteMission(t, catalog) })
	t.Run("installation", func(t *testing.T) { checkInstallAgentMission(t, catalog) })
	t.Run("build and optional publication", func(t *testing.T) { checkBuildAgentMission(t, catalog) })

	override := fixtures.MissionJSON("install-agent", "Keep the operator's saved definition")
	fixture.ReplaceMissionAndResetRequests("install-agent", json.RawMessage(override))
	output, err = missionCommand(t, fixture.Connection(), nil, "missions", "seed", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if result := importResult(t, output); result.Counts.Created != 0 || result.Counts.Skipped != 3 {
		t.Fatalf("repeated seed did not preserve saved definitions: %s", output)
	}
	state = fixture.Snapshot()
	if len(state.Records) != 4 || string(state.Records["install-agent"]) != override || string(state.Records["operator-mission"]) != other {
		t.Fatal("seed changed a saved mission")
	}
	for _, request := range state.Requests {
		if !strings.HasPrefix(request, "GET ") {
			t.Errorf("repeated seed submitted a write: %s", request)
		}
	}
}

func checkWebsiteMission(t *testing.T, websites map[string]missions.Mission) {
	website := websites["review-website"]
	discovery := []string{"discover-pages-no-browser"}
	required := []string{"discover-pages-no-browser", "check-links", "check-seo", "detect-technologies"}
	optional := []string{"check-accessibility", "capture-page"}
	aggregate := website.Rules[missions.AggregateResults]
	decision := website.Rules[missions.DecideNextActions]
	if !slices.Equal(website.StartingActions, discovery) || !slices.Equal(website.RequiredActions, required) || !slices.Equal(aggregate.ForActions, discovery) {
		t.Fatalf("website HTTP checks are not grouped automatic work: %+v", website)
	}
	if !slices.Equal(website.OptionalActions, optional) {
		t.Fatalf("website browser checks must remain optional: %+v", website)
	}
	if len(website.Rules) != 2 {
		t.Fatalf("unexpected mission rules: %+v", website.Rules)
	}
	checks := []string{"check-links", "check-accessibility", "capture-page", "check-seo", "detect-technologies"}
	if !slices.Equal(decision.ForActions, checks) || decision.Params == nil || decision.Params.DecisionBy != missions.Model {
		t.Fatalf("website checks are not followed by model review: %+v", decision)
	}
}

func checkInstallAgentMission(t *testing.T, catalog map[string]missions.Mission) {
	mission := catalog["install-agent"]
	if !slices.Equal(mission.RequiredActions, []string{"spawn-agent"}) || !slices.Equal(mission.StartingActions, []string{"spawn-agent"}) || len(mission.Steps) != 0 {
		t.Fatalf("agent installation must work before business agents register: %+v", mission)
	}
	decision := mission.Rules[missions.DecideNextActions]
	if len(mission.Rules) != 1 || !slices.Equal(decision.ForActions, []string{"spawn-agent"}) || decision.Params == nil || decision.Params.DecisionBy != missions.Model {
		t.Fatalf("spawn completion must trigger result and readiness review: %+v", mission.Rules)
	}
	for _, input := range []string{
		`{"package":"github.com/defos-ai/commander-web-agent@v0.2.0"}`,
		`{"package":"https://github.com/defos-ai/commander-web-agent"}`,
		`{"package":"github.com/defos-ai/commander-web-agent"}`,
		`{"package":"github.com/defos-ai/commander/apps/agents/builder@stable"}`,
		`{"package":"https://github.com/example/private-agent","visibility":"private"}`,
		`{"package":"github.com/defos-ai/commander/apps/agents/builder@v0.2.0"}`,
		`{"package":"github.com/defos-ai/commander-web-agent@v0.2.0","visibility":"public"}`,
		`{"package":"github.com/example/private-agent@v0.2.0","visibility":"private"}`,
	} {
		if err := mission.ValidateInput(json.RawMessage(input)); err != nil {
			t.Fatalf("package rejected: %v", err)
		}
		if err := mission.ValidateOutput(json.RawMessage(input), json.RawMessage(`{"agent_id":"web-agent-1234abcd","limitations":["Actions still need configuration"]}`)); err != nil {
			t.Fatalf("registration with readiness limitations rejected: %v", err)
		}
	}
	for _, input := range []string{
		`{}`,
		`{"package":"github.com/defos-ai/commander-web-agent@latest"}`,
		`{"package":"github.com/defos-ai/commander-web-agent@v0.2.0","environment":{"KEY":"value"}}`,
		`{"package":"github.com/example/private-agent@v0.2.0","visibility":"internal"}`,
		`{"package":"github.com/example/private-agent@v0.2.0","visibility":""}`,
	} {
		if err := mission.ValidateInput(json.RawMessage(input)); err == nil {
			t.Fatalf("invalid installation input accepted: %s", input)
		}
	}
}

func checkBuildAgentMission(t *testing.T, catalog map[string]missions.Mission) {
	if _, exists := catalog["publish-agent"]; exists {
		t.Fatal("source publication must remain an optional action in build-agent")
	}
	if _, exists := catalog["release-agent"]; exists {
		t.Fatal("release must remain an optional action in build-agent")
	}
	mission := catalog["build-agent"]
	if len(mission.Objectives) < 2 {
		t.Fatal("build guidance must be grouped into separate objectives")
	}
	stages := []string{"checkout-template", "build-agent"}
	if !slices.Equal(mission.RequiredActions, stages) || !slices.Equal(mission.OptionalActions, []string{"publish-agent", "release-agent"}) || !slices.Equal(mission.StartingActions, stages[:1]) {
		t.Fatalf("build mission lost its checkout prerequisite: %+v", mission)
	}
	affinity, decision := mission.Rules[missions.SameAgent], mission.Rules[missions.DecideNextActions]
	if len(mission.Rules) != 2 || !slices.Equal(affinity.ForActions, stages) || !slices.Equal(decision.ForActions, []string{"checkout-template", "build-agent", "publish-agent"}) || decision.Params == nil || decision.Params.DecisionBy != missions.Model {
		t.Fatalf("workspace stages need affinity and build/publication review; release polls must continue automatically: %+v", mission.Rules)
	}
	const requestedActions = `[{"name":"validate-json","description":"Validate supplied JSON.","input_schema":{"type":"object"},"output_schema":{"type":"object"}}]`
	input := json.RawMessage(`{"instructions":"Create a JSON validation agent.","actions":` + requestedActions + `}`)
	if err := mission.ValidateInput(input); err != nil {
		t.Fatal(err)
	}
	if err := mission.ValidateOutput(input, json.RawMessage(`{"workspace_id":"checkout-example","summary":"Generated the agent and validated its source."}`)); err != nil {
		t.Fatal(err)
	}
	publicationInput := json.RawMessage(`{"instructions":"Create a JSON validation agent.","actions":` + requestedActions + `,"repository":"example/json-agent","visibility":"public"}`)
	if err := mission.ValidateInput(publicationInput); err != nil {
		t.Fatalf("optional publication preferences rejected: %v", err)
	}
	publicationOutput := json.RawMessage(`{"workspace_id":"checkout-example","summary":"Generated and published the source.","publication":{"repository_url":"https://github.com/example/json-agent","commit_url":"https://github.com/example/json-agent/commit/0123456789abcdef0123456789abcdef01234567","commit_sha":"0123456789abcdef0123456789abcdef01234567","visibility":"public","branch":"main"}}`)
	if err := mission.ValidateOutput(publicationInput, publicationOutput); err != nil {
		t.Fatalf("publication result rejected: %v", err)
	}
	if err := mission.ValidateOutput(publicationInput, json.RawMessage(`{"workspace_id":"checkout-example","summary":"Generated the source without publishing."}`)); err != nil {
		t.Fatalf("publication must remain optional in the result: %v", err)
	}
	if err := mission.ValidateOutput(input, json.RawMessage(`{"workspace_id":"checkout-example","summary":"Generated the source.","publication":{"repository_url":"https://github.com/example/json-agent"}}`)); err == nil {
		t.Fatal("incomplete publication result accepted")
	}
	releaseInput := json.RawMessage(`{"instructions":"Create a JSON validation agent.","actions":` + requestedActions + `,"repository":"example/json-agent","visibility":"public","release_version":"v0.1.0"}`)
	if err := mission.ValidateInput(releaseInput); err != nil {
		t.Fatalf("explicit optional release request rejected: %v", err)
	}
	releaseOutput := json.RawMessage(`{"workspace_id":"checkout-example","summary":"Released the reviewed source.","release":{"repository":"example/json-agent","commit_sha":"0123456789abcdef0123456789abcdef01234567","version":"v0.1.0","agent_type":"json-agent","visibility":"public","status":"released","workflow_run_id":123,"workflow_url":"https://github.com/example/json-agent/actions/runs/123","conclusion":"success","package":"github.com/example/json-agent@v0.1.0","release_url":"https://github.com/example/json-agent/releases/tag/v0.1.0"}}`)
	if err := mission.ValidateOutput(releaseInput, releaseOutput); err != nil {
		t.Fatalf("verified release result rejected: %v", err)
	}
	if err := mission.ValidateOutput(releaseInput, json.RawMessage(`{"workspace_id":"checkout-example","summary":"Build finished; publication and release were skipped."}`)); err != nil {
		t.Fatalf("release must remain optional in the result: %v", err)
	}
	if err := mission.ValidateOutput(releaseInput, json.RawMessage(`{"workspace_id":"checkout-example","summary":"Still waiting.","release":{"repository":"example/json-agent","commit_sha":"0123456789abcdef0123456789abcdef01234567","version":"v0.1.0","agent_type":"json-agent","visibility":"public","status":"pending"}}`)); err == nil {
		t.Fatal("pending release accepted as a finished mission report")
	}
	for _, invalid := range []string{
		`{}`,
		`{"instructions":"Build an agent"}`,
		`{"instructions":"Build an agent","actions":[]}`,
		`{"instructions":" ","actions":` + requestedActions + `}`,
		`{"instructions":"Build an agent","actions":` + requestedActions + `,"OPENAI_API_KEY":"value"}`,
		`{"instructions":"Build an agent","actions":` + requestedActions + `,"repository":"example/json-agent","visibility":"internal"}`,
		`{"instructions":"Build an agent","actions":` + requestedActions + `,"release_version":"stable"}`,
		`{"instructions":"Build an agent","actions":` + requestedActions + `,"release_version":"v0.1.0-rc.1"}`,
	} {
		if err := mission.ValidateInput(json.RawMessage(invalid)); err == nil {
			t.Fatalf("invalid build input accepted: %s", invalid)
		}
	}
}

type missionImportResult struct {
	Items []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	} `json:"items"`
	Counts struct {
		Created      int `json:"created"`
		Skipped      int `json:"skipped"`
		WouldCreate  int `json:"would_create"`
		Uncertain    int `json:"uncertain"`
		NotAttempted int `json:"not_attempted"`
	} `json:"counts"`
}

func importResult(t *testing.T, output []byte) missionImportResult {
	t.Helper()
	var result missionImportResult
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("invalid import JSON: %s (%v)", output, err)
	}
	return result
}

func missionCommand(t *testing.T, connection client.Connection, input io.Reader, args ...string) ([]byte, error) {
	t.Helper()
	var output, diagnostics bytes.Buffer
	err := cli.RunWithOptions(t.Context(), args, input, &output, &diagnostics, cli.Options{Connection: &connection})
	if strings.Contains(output.String()+diagnostics.String(), connection.Credentials.Password) && connection.Credentials.Password != "" {
		t.Fatal("command exposed API credentials")
	}
	return output.Bytes(), err
}

func assertMissionJSON(t *testing.T, data []byte, name string) {
	t.Helper()
	var definition struct {
		Name        string          `json:"name"`
		InputSchema json.RawMessage `json:"input_schema"`
	}
	if json.Unmarshal(data, &definition) != nil || definition.Name != name || !bytes.Contains(definition.InputSchema, []byte("9007199254740993")) || !bytes.Contains(definition.InputSchema, []byte("1e+09")) {
		t.Fatalf("mission name or schema number representation changed: %s", data)
	}
}
