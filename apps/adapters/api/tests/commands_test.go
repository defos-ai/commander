package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"commander/framework/comms/clients/controller/commands"
	"commander/framework/comms/messages"
	"commander/framework/controller"
	adapterconsumer "commander/framework/controller/consumers/adapter"
	"commander/framework/controller/runner"
	"commander/framework/domain"
	"commander/framework/domain/executions"
	"commander/framework/domain/missions"
	"commander/framework/domain/operations"
	"commander/framework/id"
	fsactions "commander/providers/fs/actions"
	fsagents "commander/providers/fs/agents"
	fsartifacts "commander/providers/fs/artifacts"
	fscomms "commander/providers/fs/comms"
	fsexecutions "commander/providers/fs/executions"
	fsmissions "commander/providers/fs/missions"
	fsreports "commander/providers/fs/reports"
)

func TestOperationCommandsPersistStartAssignAndCancelThroughController(t *testing.T) {
	fixture := newFixture(t)
	startCommandController(t, fixture, nil)
	server := startAPI(t, fixture.localArgs())
	operationID := recordID(100)
	base := "/api/v1/operations/" + operationID
	startBody := fmt.Sprintf(`{"operation_id":%q,"mission_name":"review","input":{"url":"https://example.test"}}`, operationID)

	created := server.request(t, http.MethodPost, "/api/v1/operations", authenticate, commandBody(startBody, "application/json; charset=utf-8"))
	op := decodeResponse[operations.Operation](t, created, http.StatusCreated)
	stored := decodeResponse[operations.Operation](t, server.request(t, http.MethodGet, base, authenticate))

	if created.header.Get("Location") != base || op.ID != operationID || op.Status != operations.Waiting || op.Preflight == nil || op.Preflight.Ready {
		t.Fatalf("start lost location, identity, or unavailable-action preflight: location=%q operation=%+v", created.header.Get("Location"), op)
	}
	if op.CreatedAt.IsZero() || op.UpdatedAt.IsZero() || len(op.Events) != 1 || op.Events[0].Type != operations.OperationStarted || !reflect.DeepEqual(op, stored) {
		t.Fatalf("start response must contain the complete persisted operation: response=%+v stored=%+v", op, stored)
	}
	before := operationBytes(t, fixture, operationID)
	assertAPIError(t, server.request(t, http.MethodPost, "/api/v1/operations", authenticate, commandBody(startBody, "application/json")), http.StatusConflict, "operation_conflict")
	if !bytes.Equal(before, operationBytes(t, fixture, operationID)) {
		t.Fatal("duplicate caller operation ID changed the existing operation")
	}

	assigned := server.request(t, http.MethodPost, base+"/tasks", authenticate, commandBody(`{"action_name":"review","input":{"url":"https://example.test/links"},"step_id":"links"}`, "application/json"))
	task := decodeResponse[operations.Task](t, assigned, http.StatusCreated)
	savedTask := decodeResponse[operations.Task](t, server.request(t, http.MethodGet, base+"/tasks/"+task.ID, authenticate))

	if assigned.header.Get("Location") != base+"/tasks/"+task.ID || !id.Valid(task.ID) || task.OperationID != operationID || task.CommandID != task.ID || task.StepID != "links" || task.ActionName != "review" {
		t.Fatalf("assignment lost its location or controller-owned identities: location=%q task=%+v", assigned.header.Get("Location"), task)
	}
	if task.Status != operations.TaskQueued || task.AgentID != "" || !reflect.DeepEqual(task, savedTask) || string(task.Input) != `{"url":"https://example.test/links"}` {
		t.Fatalf("assignment did not persist the full queued task: response=%+v stored=%+v", task, savedTask)
	}
	// Input and step are optional; the controller still generates task identity.
	optional := decodeResponse[operations.Task](t, server.request(t, http.MethodPost, base+"/tasks", authenticate, commandBody(`{"action_name":"review"}`, "application/json")), http.StatusCreated)
	if !id.Valid(optional.ID) || optional.ID == task.ID || optional.OperationID != operationID || optional.Status != operations.TaskQueued {
		t.Fatalf("assignment with omitted optional fields failed: %+v", optional)
	}

	cancelled := decodeResponse[operations.Operation](t, server.request(t, http.MethodPost, base+"/cancel", authenticate, commandBody(`{}`, "application/json")))
	stored = decodeResponse[operations.Operation](t, server.request(t, http.MethodGet, base, authenticate))
	events := decodeResponse[eventPage](t, server.request(t, http.MethodGet, base+"/events", authenticate))
	listed := decodeResponse[struct {
		Items []operations.Operation `json:"items"`
	}](t, server.request(t, http.MethodGet, "/api/v1/operations", authenticate))

	if cancelled.Status != operations.Cancelled || !reflect.DeepEqual(cancelled, stored) || !reflect.DeepEqual(listed.Items, []operations.Operation{cancelled}) {
		t.Fatalf("cancel response and persisted operation disagree: response=%+v stored=%+v list=%+v", cancelled, stored, listed.Items)
	}
	if len(cancelled.Tasks) != 2 {
		t.Fatalf("cancel lost assigned tasks: %+v", cancelled.Tasks)
	}
	for _, task := range cancelled.Tasks {
		if task.Status != operations.TaskCancelled || task.FinishedAt == nil || task.AgentID != "" {
			t.Fatalf("cancel did not finish the queued task: %+v", task)
		}
	}
	if !reflect.DeepEqual(events.Items, cancelled.Events) || len(events.Items) != 4 || events.Items[1].Type != operations.TaskAssigned || events.Items[1].TaskID != task.ID || events.Items[2].Type != operations.TaskAssigned || events.Items[3].Type != operations.OperationCancelled || events.NextCursor != "4" {
		t.Fatalf("command events do not describe the persisted lifecycle: %+v", events)
	}
	before = operationBytes(t, fixture, operationID)
	assertAPIError(t, server.request(t, http.MethodPost, base+"/tasks", authenticate, commandBody(`{"action_name":"review"}`, "application/json")), http.StatusConflict, "operation_finished")
	assertAPIError(t, server.request(t, http.MethodPost, base+"/cancel", authenticate), http.StatusConflict, "operation_finished")
	if !bytes.Equal(before, operationBytes(t, fixture, operationID)) {
		t.Fatal("commands against a finished operation changed persisted state")
	}
}

func TestOperationCommandsAcceptJSONInputsAndEmptyCancellation(t *testing.T) {
	fixture := newFixture(t)
	startCommandController(t, fixture, nil)
	server := startAPI(t, fixture.localArgs())
	inputs := []string{`null`, `{}`, `["page",2]`, `"https://example.test"`, `42`, `true`}

	for index, input := range inputs {
		t.Run(input, func(t *testing.T) {
			optionalID := ""
			if index%2 != 0 {
				optionalID = `,"operation_id":null`
			}
			created := server.request(t, http.MethodPost, "/api/v1/operations", authenticate, commandBody(`{"mission_name":"any-input","input":`+input+optionalID+`}`, "application/json"))
			op := decodeResponse[operations.Operation](t, created, http.StatusCreated)

			if !id.Valid(op.ID) || created.header.Get("Location") != "/api/v1/operations/"+op.ID || string(op.Input) != input {
				t.Fatalf("start did not generate a canonical ID and preserve JSON input: location=%q operation=%+v", created.header.Get("Location"), op)
			}
			configure := []func(*http.Request){authenticate}
			switch index % 3 {
			case 1:
				configure = append(configure, commandBody("", ""))
			case 2:
				configure = append(configure, commandBody(" \n\t", ""))
			}
			cancelled := decodeResponse[operations.Operation](t, server.request(t, http.MethodPost, "/api/v1/operations/"+op.ID+"/cancel", configure...))
			if cancelled.ID != op.ID || cancelled.Status != operations.Cancelled {
				t.Fatalf("empty cancellation did not cancel the created operation: %+v", cancelled)
			}
		})
	}
}

func TestOperationCommandsRejectPayloadsWithoutMutation(t *testing.T) {
	fixture := newFixture(t)
	startCommandController(t, fixture, nil)
	server := startAPI(t, fixture.localArgs())
	op := decodeResponse[operations.Operation](t, server.request(t, http.MethodPost, "/api/v1/operations", authenticate, commandBody(`{"mission_name":"review","input":{"url":"https://example.test"}}`, "application/json")), http.StatusCreated)
	base := "/api/v1/operations/" + op.ID
	before := operationBytes(t, fixture, op.ID)
	paths := []string{"/api/v1/operations", base + "/tasks", base + "/cancel"}

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			assertAPIError(t, server.request(t, http.MethodPost, path), http.StatusUnauthorized, "authentication_required")
			for _, body := range []string{`{`, `{} {}`, `{} null`, `[]`, `null`, `"value"`, `17`, `true`, `{"unknown":true}`} {
				assertAPIError(t, server.request(t, http.MethodPost, path, authenticate, commandBody(body, "application/json")), http.StatusBadRequest, "invalid_request")
			}
			for _, contentType := range []string{"", "text/plain", "application/problem+json", "application/json; charset"} {
				assertAPIError(t, server.request(t, http.MethodPost, path, authenticate, commandBody(`{}`, contentType)), http.StatusUnsupportedMediaType, "unsupported_media_type")
			}
			oversized := `{"padding":"` + strings.Repeat("x", 1<<20) + `"}`
			for _, chunked := range []bool{false, true} {
				configure := []func(*http.Request){authenticate, commandBody(oversized, "application/json")}
				if chunked {
					configure = append(configure, func(request *http.Request) { request.ContentLength = -1 })
				}
				assertAPIError(t, server.request(t, http.MethodPost, path, configure...), http.StatusRequestEntityTooLarge, "request_too_large")
			}
		})
	}
	for _, body := range []string{
		``, `{}`, `{"mission_name":"review"}`, `{"mission_name":" ","input":{}}`, `{"mission_name":17,"input":{}}`,
		`{"operation_id":"not-a-uuid","mission_name":"review","input":{}}`,
		`{"operation_id":"00000000-0000-4000-8000-000000ABCDEF","mission_name":"review","input":{}}`,
		`{"mission_name":"review","input":{},"agent_id":"review-agent-1234abcd"}`,
	} {
		assertAPIError(t, server.request(t, http.MethodPost, paths[0], authenticate, commandBody(body, "application/json")), http.StatusBadRequest, "invalid_request")
	}
	for _, body := range []string{
		``, `{}`, `{"action_name":" "}`, `{"action_name":17}`, `{"action_name":"review","input":null}`, `{"action_name":"review","input":[]}`,
		`{"action_name":"review","input":"value"}`, `{"action_name":"review","step_id":17}`,
		`{"action_name":"review","agent_id":"review-agent-1234abcd"}`,
		fmt.Sprintf(`{"action_name":"review","operation_id":%q}`, op.ID),
	} {
		assertAPIError(t, server.request(t, http.MethodPost, paths[1], authenticate, commandBody(body, "application/json")), http.StatusBadRequest, "invalid_request")
	}
	for _, body := range []string{`{"reason":"stop"}`, fmt.Sprintf(`{"operation_id":%q}`, op.ID)} {
		assertAPIError(t, server.request(t, http.MethodPost, paths[2], authenticate, commandBody(body, "application/json")), http.StatusBadRequest, "invalid_request")
	}
	for _, invalid := range []string{"not-a-uuid", strings.ToUpper(recordID(0xabcdef)), strings.ReplaceAll(op.ID, "-", "")} {
		for _, suffix := range []string{"/tasks", "/cancel"} {
			body := `{}`
			if suffix == "/tasks" {
				body = `{"action_name":"review"}`
			}
			assertAPIError(t, server.request(t, http.MethodPost, "/api/v1/operations/"+invalid+suffix, authenticate, commandBody(body, "application/json")), http.StatusBadRequest, "invalid_operation_id")
		}
	}

	listed := decodeResponse[struct {
		Items []operations.Operation `json:"items"`
	}](t, server.request(t, http.MethodGet, "/api/v1/operations", authenticate))
	if len(listed.Items) != 1 || listed.Items[0].ID != op.ID || !bytes.Equal(before, operationBytes(t, fixture, op.ID)) {
		t.Fatal("rejected command payloads created operations or changed persisted operation state")
	}
}

func TestOperationCommandsReturnControllerDomainErrors(t *testing.T) {
	fixture := newFixture(t)
	execution := startCommandController(t, fixture, nil)
	server := startAPI(t, fixture.localArgs())
	op := decodeResponse[operations.Operation](t, server.request(t, http.MethodPost, "/api/v1/operations", authenticate, commandBody(`{"mission_name":"review","input":{"url":"https://example.test"}}`, "application/json")), http.StatusCreated)
	base := "/api/v1/operations/" + op.ID
	before := operationBytes(t, fixture, op.ID)

	assertAPIError(t, server.request(t, http.MethodPost, "/api/v1/operations", authenticate, commandBody(`{"mission_name":"missing-mission","input":{}}`, "application/json")), http.StatusNotFound, "mission_not_found")
	for _, input := range []string{`{}`, `{"url":42}`, `null`} {
		assertAPIError(t, server.request(t, http.MethodPost, "/api/v1/operations", authenticate, commandBody(`{"mission_name":"review","input":`+input+`}`, "application/json")), http.StatusUnprocessableEntity, "invalid_input")
	}
	assertAPIError(t, server.request(t, http.MethodPost, base+"/tasks", authenticate, commandBody(`{"action_name":"review","step_id":"missing-step"}`, "application/json")), http.StatusUnprocessableEntity, "invalid_input")
	missing := "/api/v1/operations/" + recordID(999)
	assertAPIError(t, server.request(t, http.MethodPost, missing+"/tasks", authenticate, commandBody(`{"action_name":"review"}`, "application/json")), http.StatusNotFound, "operation_not_found")
	assertAPIError(t, server.request(t, http.MethodPost, missing+"/cancel", authenticate), http.StatusNotFound, "operation_not_found")
	if !bytes.Equal(before, operationBytes(t, fixture, op.ID)) {
		t.Fatal("rejected controller commands changed the saved operation")
	}
	listed := decodeResponse[struct {
		Items []operations.Operation `json:"items"`
	}](t, server.request(t, http.MethodGet, "/api/v1/operations", authenticate))
	if len(listed.Items) != 1 {
		t.Fatalf("invalid mission inputs created operations: %+v", listed.Items)
	}

	if _, err := execution.Ask(t.Context(), commands.AskQuestionCommand{OperationID: op.ID, Text: "Continue review?", Options: []string{"yes", "no"}}); err != nil {
		t.Fatal(err)
	}
	before = operationBytes(t, fixture, op.ID)
	assertAPIError(t, server.request(t, http.MethodPost, base+"/tasks", authenticate, commandBody(`{"action_name":"review"}`, "application/json")), http.StatusConflict, "question_pending")
	if !bytes.Equal(before, operationBytes(t, fixture, op.ID)) {
		t.Fatal("assignment blocked by a pending question changed operation state")
	}
}

func TestOperationCommandsMapBusErrorsWithoutRetryOrPrivateDetails(t *testing.T) {
	fixture := newFixture(t)
	cases := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"uncertain", operations.ErrWriteUncertain, http.StatusServiceUnavailable, "operation_write_uncertain"},
		{"uncertain-timeout", errors.Join(context.DeadlineExceeded, operations.ErrWriteUncertain), http.StatusServiceUnavailable, "operation_write_uncertain"},
		{"indeterminate-timeout", errors.Join(context.DeadlineExceeded, executions.ErrIndeterminate), http.StatusServiceUnavailable, "command_outcome_unknown"},
		{"controller-closed", domain.ErrControllerClosed, http.StatusServiceUnavailable, "controller_unavailable"},
		{"runner-closed", operations.ErrClosed, http.StatusServiceUnavailable, "controller_unavailable"},
		{"canceled", context.Canceled, http.StatusServiceUnavailable, "request_canceled"},
		{"timeout", context.DeadlineExceeded, http.StatusGatewayTimeout, "request_timeout"},
		{"unexpected", errors.New("private database path and credentials"), http.StatusInternalServerError, "internal_error"},
	}
	failures := &failingStarts{failures: map[string]error{}, attempts: make(chan commandAttempt, len(cases)+1)}
	for _, tc := range cases {
		failures.failures[tc.name] = fmt.Errorf("private-controller-details: %w", tc.err)
	}
	startCommandController(t, fixture, func(execution commands.Operations) commands.Operations {
		failures.Operations = execution
		return failures
	})
	server := startAPI(t, fixture.localArgs())

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := server.request(t, http.MethodPost, "/api/v1/operations", authenticate, commandBody(fmt.Sprintf(`{"mission_name":%q,"input":{}}`, tc.name), "application/json"))

			assertAPIError(t, result, tc.status, tc.code)
			attempt := <-failures.attempts
			if attempt.mission != tc.name || attempt.remaining <= 0 || attempt.remaining > 10*time.Second {
				t.Fatalf("controller command had the wrong request or timeout: %+v", attempt)
			}
			select {
			case extra := <-failures.attempts:
				t.Fatalf("HTTP command was retried after failure: %+v", extra)
			default:
			}
		})
	}
}

func commandBody(body, contentType string) func(*http.Request) {
	return func(request *http.Request) {
		request.Body = io.NopCloser(strings.NewReader(body))
		request.ContentLength = int64(len(body))
		if contentType != "" {
			request.Header.Set("Content-Type", contentType)
		}
	}
}

// The API opens its own clients against these same filesystem stores and bus.
// No agent is registered, so missing preflight requirements keep tasks queued.
func startCommandController(t *testing.T, fixture fixture, decorate func(commands.Operations) commands.Operations) *runner.Runner {
	t.Helper()
	ctx := context.WithoutCancel(t.Context())
	database := filepath.Join(fixture.root, "database")
	bus, err := fscomms.NewFsBus(filepath.Join(fixture.root, "bus"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := bus.Close(); err != nil {
			t.Errorf("close controller bus: %v", err)
		}
	})
	artifacts, err := fsartifacts.NewArtifactFsStore(database)
	if err != nil {
		t.Fatal(err)
	}
	agents, err := fsagents.NewAgentFsStore(database)
	if err != nil {
		t.Fatal(err)
	}
	actions, err := fsactions.NewActionFsStore(database)
	if err != nil {
		t.Fatal(err)
	}
	executions, err := fsexecutions.NewExecutionFsStore(database)
	if err != nil {
		t.Fatal(err)
	}
	missionStore, err := fsmissions.NewMissionFsStore(database)
	if err != nil {
		t.Fatal(err)
	}
	reports, err := fsreports.NewReportFsStore(filepath.Join(fixture.root, "storage"))
	if err != nil {
		t.Fatal(err)
	}
	mission := missions.Mission{Name: "review", Objective: "Review website links", RequiredActions: []string{"review"}, StartingActions: []string{"review"},
		Steps:       []missions.Step{{ID: "links", Objective: "Review links", RequiredActions: []string{"review"}}},
		InputSchema: json.RawMessage(`{"type":"object","properties":{"url":{"type":"string"}},"required":["url"]}`), OutputSchema: json.RawMessage(`{}`)}
	if err := missionStore.Upsert(ctx, mission); err != nil {
		t.Fatal(err)
	}
	mission.Name, mission.InputSchema = "any-input", json.RawMessage(`{}`)
	if err := missionStore.Upsert(ctx, mission); err != nil {
		t.Fatal(err)
	}
	coordination, err := controller.NewController(ctx, bus, controller.ControllerConfig{ArtifactStore: artifacts, AgentStore: agents, ActionStore: actions, ExecutionStore: executions})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		shutdown, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := coordination.Close(shutdown); err != nil {
			t.Errorf("close controller: %v", err)
		}
	})
	execution, err := runner.NewRunner(ctx, coordination, runner.Config{MissionStore: missionStore, OperationStore: operationStore(t, fixture), ReportStore: reports, AgentCatalog: coordination})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		shutdown, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := execution.Close(shutdown); err != nil {
			t.Errorf("close runner: %v", err)
		}
	})
	var operationCommands commands.Operations = execution
	if decorate != nil {
		operationCommands = decorate(execution)
	}
	consumer, err := adapterconsumer.NewConsumer(ctx, bus, messages.DefaultControllerType, coordination.ID(), coordination, operationCommands, nil)
	if err != nil {
		t.Fatal(err)
	}
	commandCtx, cancelCommands := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- consumer.Consume(commandCtx) }()
	t.Cleanup(func() {
		cancelCommands()
		if err := consumer.Close(); err != nil {
			t.Errorf("close adapter consumer: %v", err)
		}
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Errorf("adapter consumer stopped: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("adapter consumer did not stop within five seconds")
		}
	})
	return execution
}

type commandAttempt struct {
	mission   string
	remaining time.Duration
}

type failingStarts struct {
	commands.Operations
	failures map[string]error
	attempts chan commandAttempt
}

func (f *failingStarts) Start(ctx context.Context, command commands.StartOperationCommand) (operations.Operation, error) {
	deadline, _ := ctx.Deadline()
	f.attempts <- commandAttempt{mission: command.MissionName, remaining: time.Until(deadline)}
	return operations.Operation{}, f.failures[command.MissionName]
}
