package e2e_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	e2efixtures "github.com/defos-ai/commander/apps/adapters/api/tests/fixtures/e2e"
	"github.com/defos-ai/commander/framework/domain/missions/policies"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/domain/sessions"
	"github.com/defos-ai/commander/framework/id"
)

func TestE2ESessionPolicyAssignmentsAndContinuations(t *testing.T) {
	peer := e2efixtures.ImmediateSessionPeer(t)
	s := e2efixtures.NewSessionStack(t, peer.URL)
	s.Worker(1)
	s.SessionMission("policy-paths", policies.SessionPolicy{CreateAction: "connect-resource", UseActions: []string{"use-resource"}})
	op := s.Start("policy-paths")
	path := "/api/v1/operations/" + op

	s.Request("POST", path+"/tasks", map[string]any{"action_name": "connect-resource", "input": map[string]string{"session_scope": id.New()}}, 422, nil)
	s.Request("POST", path+"/tasks", map[string]any{"action_name": "connect-resource", "input": map[string]string{"session_id": id.New()}}, 422, nil)
	s.Request("POST", path+"/tasks", map[string]any{"action_name": "use-resource", "input": map[string]any{}}, 422, nil)
	marker := s.Assign(op, "mark-owner", map[string]any{})
	marker = s.Finished(op, marker.ID)
	s.Request("POST", path+"/tasks/"+marker.ID+"/continuation/resolve", map[string]any{
		"version": marker.Continuation.Version, "actions": []any{
			map[string]any{"action_name": "connect-resource", "input": map[string]any{}},
			map[string]any{"action_name": "use-resource", "input": map[string]any{}},
		},
	}, 422, nil)
	if current := s.Operation(op); len(current.Tasks) != 1 {
		t.Fatalf("rejected session work changed accepted tasks: %+v", current.Tasks)
	}
	input := json.RawMessage(`{"payload":{"limit":9007199254740993,"escaped":"\u0061"}}`)
	creation := s.Assign(op, "connect-resource", input)
	creation = s.Finished(op, creation.ID)
	resource := requirePolicySession(t, creation)
	var accepted map[string]json.RawMessage
	if err := json.Unmarshal(creation.Input, &accepted); err != nil {
		t.Fatal(err)
	}
	if _, supplied := accepted["session_id"]; supplied || !bytes.Contains(creation.Input, []byte(`9007199254740993`)) || !bytes.Contains(creation.Input, []byte(`"\u0061"`)) {
		t.Fatalf("creation injection changed business JSON or requested reuse: %s", creation.Input)
	}
	s.Request("POST", path+"/tasks", map[string]any{"action_name": "connect-resource", "input": map[string]any{}}, 422, nil)
	for _, conflicting := range []map[string]string{
		{"session_scope": id.New()},
		{"session_id": id.New()},
		{"session_scope": resource.ScopeID, "session_id": id.New()},
	} {
		s.Request("POST", path+"/tasks", map[string]any{"action_name": "use-resource", "input": conflicting}, 422, nil)
	}
	for _, ambiguous := range []json.RawMessage{
		json.RawMessage(`{"SESSION_SCOPE":"` + resource.ScopeID + `","session_id":"` + resource.ID + `"}`),
		json.RawMessage(`{"session_scope":"` + resource.ScopeID + `","session_\u0073cope":"` + resource.ScopeID + `","session_id":"` + resource.ID + `"}`),
		json.RawMessage(`{"session_scope":"` + resource.ScopeID + `","session_id":"` + resource.ID + `","session_id":"` + resource.ID + `"}`),
	} {
		s.Request("POST", path+"/tasks", map[string]any{"action_name": "use-resource", "input": ambiguous}, 422, nil)
	}

	use := s.Assign(op, "use-resource", map[string]any{})
	used := s.Finished(op, use.ID)
	var resolved operations.Operation
	s.Request("POST", path+"/tasks/"+marker.ID+"/continuation/resolve", map[string]any{
		"version": marker.Continuation.Version, "actions": []any{map[string]any{"action_name": "use-resource", "input": map[string]any{}}},
	}, 200, &resolved)
	var modelChild operations.Task
	for _, task := range resolved.Tasks {
		if task.ParentTaskID == marker.ID {
			modelChild = task
		}
	}
	if modelChild.ID == "" {
		t.Fatal("model continuation did not retain its session task")
	}
	modelChild = s.Finished(op, modelChild.ID)
	automatic := s.Assign(op, "propose-work", map[string]string{"action_name": "use-resource"})
	s.Finished(op, automatic.ID)
	var automaticChild operations.Task
	e2efixtures.WaitUntil(t, 10*time.Second, func() bool {
		for _, task := range s.Operation(op).Tasks {
			if task.ParentTaskID == automatic.ID {
				automaticChild = task
				return true
			}
		}
		return false
	})
	automaticChild = s.Finished(op, automaticChild.ID)
	missingCreator := s.Start("policy-paths")
	missingParent := s.Assign(missingCreator, "propose-work", map[string]string{"action_name": "use-resource"})
	missingParent = s.Finished(missingCreator, missingParent.ID)
	var invalidChild operations.Task
	e2efixtures.WaitUntil(t, 10*time.Second, func() bool {
		for _, task := range s.Operation(missingCreator).Tasks {
			if task.ParentTaskID == missingParent.ID {
				invalidChild = task
				return true
			}
		}
		return false
	})
	s.Restart()
	retained := s.Assign(op, "use-resource", map[string]string{"session_scope": resource.ScopeID, "session_id": resource.ID})
	retained = s.Finished(op, retained.ID)

	for _, task := range []operations.Task{used, modelChild, automaticChild, retained} {
		requirePolicyUse(t, task, creation)
	}
	if marker.SessionIntent != nil {
		t.Fatalf("stateless work acquired the policy resource: %+v", marker.SessionIntent)
	}
	if invalidChild.Status != operations.TaskQueued || invalidChild.SessionIntent == nil || invalidChild.SessionIntent.Error == "" || invalidChild.AgentID != "" {
		t.Fatalf("invalid automatic consumer escaped admission or lost its parent outcome: parent=%+v child=%+v", missingParent, invalidChild)
	}
	if saved := s.Task(missingCreator, invalidChild.ID); saved.SessionIntent == nil || saved.SessionIntent.Error != invalidChild.SessionIntent.Error || saved.Status != operations.TaskQueued || saved.AgentID != "" {
		t.Fatalf("restart changed the rejected automatic intent: %+v", saved)
	}
}

func TestE2ESessionPolicyGroupsKeepAcceptedBindingsAfterReplacement(t *testing.T) {
	peer := e2efixtures.ImmediateSessionPeer(t)
	s := e2efixtures.NewSessionStack(t, peer.URL)
	s.Worker(1, "FIXTURE_POLICY_ACTIONS=true", "FIXTURE_SESSION_STATE=listening")
	s.SessionMission("policy-groups",
		policies.SessionPolicy{CreateAction: "connect-resource", UseActions: []string{"use-resource"}},
		policies.SessionPolicy{CreateAction: "connect-secondary", UseActions: []string{"use-secondary"}},
	)
	op := s.Start("policy-groups")
	first := s.Assign(op, "connect-resource", map[string]any{})
	first = s.Finished(op, first.ID)
	firstResource := requirePolicySession(t, first)
	blocked := s.Assign(op, "use-resource", map[string]any{})
	s.Worker(2, "FIXTURE_POLICY_ACTIONS=true")
	second := s.Assign(op, "connect-secondary", map[string]any{})
	second = s.Finished(op, second.ID)
	secondResource := requirePolicySession(t, second)
	otherOp := s.Start("policy-groups")
	other := s.Assign(otherOp, "connect-resource", map[string]any{})
	other = s.Finished(otherOp, other.ID)
	otherResource := requirePolicySession(t, other)

	if firstResource.ScopeID == secondResource.ScopeID || firstResource.ScopeID == otherResource.ScopeID || secondResource.ScopeID == otherResource.ScopeID {
		t.Fatalf("policy groups or operations shared a scope: first=%+v second=%+v other=%+v", firstResource, secondResource, otherResource)
	}
	if task := s.Task(op, blocked.ID); task.Status != operations.TaskQueued || task.AgentID != "" {
		t.Fatalf("unverified policy resource ran on a spare agent: %+v", task)
	}
	s.Control(secondResource.Reference, "verify")
	secondUse := s.Assign(op, "use-secondary", map[string]any{})
	secondUse = s.Finished(op, secondUse.ID)
	requirePolicyUse(t, secondUse, second)
	closed := s.Control(firstResource.Reference, "close")
	if closed.ReleaseReason != "closed" {
		t.Fatalf("session closure was not confirmed: %+v", closed)
	}
	replacement := s.Assign(op, "connect-resource", map[string]any{})
	replacement = s.Finished(op, replacement.ID)
	replacementResource := requirePolicySession(t, replacement)
	if replacementResource.ScopeID != firstResource.ScopeID || replacementResource.ID == firstResource.ID {
		t.Fatalf("replacement changed policy scope or reused identity: %+v", replacementResource)
	}
	s.Control(replacementResource.Reference, "verify")
	fresh := s.Assign(op, "use-resource", map[string]any{})
	fresh = s.Finished(op, fresh.ID)
	requirePolicyUse(t, fresh, replacement)
	s.Restart()

	e2efixtures.WaitUntil(t, 5*time.Second, func() bool {
		current := s.Operation(op)
		if current.Preflight == nil {
			return false
		}
		for _, placement := range current.Preflight.Tasks {
			if placement.TaskID == blocked.ID {
				return strings.Contains(placement.Reason, "session_closed")
			}
		}
		return false
	})
	retained := s.Task(op, blocked.ID)
	if retained.Status != operations.TaskQueued || retained.AgentID != "" || retained.SessionIntent == nil || retained.SessionIntent.ID != firstResource.ID || retained.SessionIntent.ScopeID != firstResource.ScopeID {
		t.Fatalf("replacement redirected accepted work: %+v", retained)
	}
}

func TestE2ESessionPolicyRejectsIncompatibleActionContracts(t *testing.T) {
	peer := e2efixtures.ImmediateSessionPeer(t)
	s := e2efixtures.NewSessionStack(t, peer.URL)
	s.Worker(1)
	s.SessionMission("policy-wrong-creator", policies.SessionPolicy{CreateAction: "mark-owner", UseActions: []string{"use-resource"}})
	s.SessionMission("policy-wrong-consumer", policies.SessionPolicy{CreateAction: "connect-resource", UseActions: []string{"mark-owner"}})
	wrongCreator := s.Start("policy-wrong-creator")
	wrongConsumer := s.Start("policy-wrong-consumer")

	s.Request("POST", "/api/v1/operations/"+wrongCreator+"/tasks", map[string]any{"action_name": "mark-owner", "input": map[string]any{}}, 422, nil)
	s.Request("POST", "/api/v1/operations/"+wrongConsumer+"/tasks", map[string]any{"action_name": "mark-owner", "input": map[string]any{}}, 422, nil)

	if len(s.Operation(wrongCreator).Tasks) != 0 || len(s.Operation(wrongConsumer).Tasks) != 0 {
		t.Fatal("incompatible session contracts reached task acceptance")
	}
}

func TestE2ESessionPolicyAutomaticCreatorsRetainAdmission(t *testing.T) {
	peer := e2efixtures.ImmediateSessionPeer(t)
	s := e2efixtures.NewSessionStack(t, peer.URL)
	s.Worker(1, "FIXTURE_POLICY_ACTIONS=true")
	s.SessionMission("policy-automatic", policies.SessionPolicy{CreateAction: "connect-resource", UseActions: []string{"use-resource"}})
	op := s.Start("policy-automatic")
	duplicateOp := s.Start("policy-automatic")

	parent := s.Assign(op, "propose-work", map[string]string{"action_name": "connect-resource"})
	parent = s.Finished(op, parent.ID)
	var creation operations.Task
	e2efixtures.WaitUntil(t, 10*time.Second, func() bool {
		for _, task := range s.Operation(op).Tasks {
			if task.ParentTaskID == parent.ID {
				creation = task
				return true
			}
		}
		return false
	})
	creation = s.Finished(op, creation.ID)
	resource := requirePolicySession(t, creation)
	duplicates := s.Assign(duplicateOp, "propose-connections", map[string]any{})
	duplicates = s.Finished(duplicateOp, duplicates.ID)
	var rejected []operations.Task
	e2efixtures.WaitUntil(t, 10*time.Second, func() bool {
		rejected = nil
		for _, task := range s.Operation(duplicateOp).Tasks {
			if task.ParentTaskID == duplicates.ID {
				rejected = append(rejected, task)
			}
		}
		return len(rejected) == 2
	})

	if parent.SessionIntent != nil || duplicates.SessionIntent != nil || duplicates.Result == nil || len(duplicates.Result.NextActions) != 2 {
		t.Fatalf("automatic creation changed its stateless parent outcome: first=%+v duplicate=%+v", parent, duplicates)
	}
	for _, task := range rejected {
		if task.Status != operations.TaskQueued || task.AgentID != "" || task.SessionIntent == nil || !strings.Contains(task.SessionIntent.Error, "multiple creations") {
			t.Fatalf("duplicate automatic creator escaped policy admission: %+v", task)
		}
	}
	var inventory struct{ Items []sessions.Session }
	s.Request("GET", "/api/v1/sessions", nil, 200, &inventory)
	if len(inventory.Items) != 1 || inventory.Items[0].ID != resource.ID {
		t.Fatalf("invalid automatic creators reserved or opened extra resources: %+v", inventory.Items)
	}
}

func TestE2ESessionPolicySelectsAnOwnerWithConsumerContracts(t *testing.T) {
	peer := e2efixtures.ImmediateSessionPeer(t)
	s := e2efixtures.NewSessionStack(t, peer.URL)
	s.Worker(1, "FIXTURE_OMIT_USE=true")
	s.Worker(2, "FIXTURE_USE_REQUIRES_SECRET=true")
	s.SessionMission("policy-owner-contracts", policies.SessionPolicy{CreateAction: "connect-resource", UseActions: []string{"use-resource"}})
	op := s.Start("policy-owner-contracts")

	creation := s.Assign(op, "connect-resource", map[string]any{})
	creation = s.Finished(op, creation.ID)
	resource := requirePolicySession(t, creation)
	use := s.Assign(op, "use-resource", map[string]any{})

	if creation.AgentID != "fixture-agent-00000002" {
		t.Fatalf("creator chose an owner that cannot execute the policy consumers: %+v", creation)
	}
	e2efixtures.WaitUntil(t, 5*time.Second, func() bool {
		current := s.Operation(op)
		if current.Preflight == nil {
			return false
		}
		for _, placement := range current.Preflight.Tasks {
			if placement.TaskID != use.ID || placement.OwnerAgentID != resource.OwnerAgentID {
				continue
			}
			for _, missing := range placement.MissingRequirements {
				if missing.Name == "SESSION_ACCESS" && missing.Kind == "secret" {
					return true
				}
			}
		}
		return false
	})
	if queued := s.Task(op, use.ID); queued.Status != operations.TaskQueued || queued.AgentID != "" || queued.SessionIntent == nil || queued.SessionIntent.ID != resource.ID {
		t.Fatalf("unready consumer lost its accepted owner binding: %+v", queued)
	}
}

func requirePolicySession(t *testing.T, task operations.Task) sessions.Session {
	t.Helper()
	if task.Result == nil || task.Result.Session == nil || task.SessionIntent == nil || task.SessionIntent.Mode != sessions.Create || task.SessionIntent.ID != task.ID || !id.Valid(task.SessionIntent.ScopeID) {
		t.Fatalf("creation did not retain a framework session claim: %+v", task)
	}
	resource := *task.Result.Session
	if resource.ID != task.SessionIntent.ID || resource.ScopeID != task.SessionIntent.ScopeID || resource.OwnerAgentID != task.AgentID {
		t.Fatalf("created resource differs from the accepted claim: task=%+v resource=%+v", task, resource)
	}
	return resource
}

func requirePolicyUse(t *testing.T, task, creation operations.Task) {
	t.Helper()
	resource := requirePolicySession(t, creation)
	if task.SessionIntent == nil || task.SessionIntent.Mode != sessions.Use || task.SessionIntent.ID != resource.ID || task.SessionIntent.ScopeID != resource.ScopeID || task.AgentID != resource.OwnerAgentID {
		t.Fatalf("use did not retain the policy resource/owner: %+v", task)
	}
	var input struct {
		Scope string `json:"session_scope"`
		ID    string `json:"session_id"`
	}
	var output struct {
		ID           string `json:"session_id"`
		CreationTask string `json:"creation_task"`
	}
	if json.Unmarshal(task.Input, &input) != nil || input.Scope != resource.ScopeID || input.ID != resource.ID || task.Result == nil ||
		json.Unmarshal(task.Result.Output, &output) != nil || output.ID != resource.ID || output.CreationTask != creation.ID {
		t.Fatalf("agent input/context disagreed with policy binding: %+v", task)
	}
}
