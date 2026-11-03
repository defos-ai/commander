package sessions

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"

	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/missions/policies"
	"github.com/defos-ai/commander/framework/domain/operations"
	model "github.com/defos-ai/commander/framework/domain/sessions"
)

var errPolicyPending = errors.New("session policy creation is awaiting reconciliation")

func policyInput(op operations.Operation, batch []actions.NextAction, action actions.NextAction, mode string, scopes []model.Scope) ([]byte, error) {
	policy, expected, matched := sessionPolicy(op, action.ActionName)
	if !matched {
		return action.Input, nil
	}
	if mode != expected {
		return nil, fmt.Errorf("session_policy_violation: action %q requires session_mode=%s", action.ActionName, expected)
	}
	if err := policyBatch(policy, batch); err != nil {
		return nil, err
	}
	scope := policyScope(op.ID, policy.CreateAction)
	record, found, err := policyRecord(op, policy.CreateAction, scope, scopes)
	if err != nil {
		return nil, err
	}
	if expected == "create" {
		for _, existing := range scopes {
			if existing.ID == scope && existing.Current != "" {
				return nil, model.ErrScopeReserved
			}
		}
		if found && record.Disposition != model.Released {
			return nil, model.ErrScopeReserved
		}
		return fillSessionInput(action.Input, scope, "")
	}
	if !found {
		return nil, errors.New("session_policy_violation: create action must be accepted before session use")
	}
	if err := record.Available(); err != nil {
		return nil, err
	}
	return fillSessionInput(action.Input, scope, record.ID)
}

func sessionPolicy(op operations.Operation, name string) (policies.SessionPolicy, string, bool) {
	if op.Mission.Policies != nil {
		for _, policy := range op.Mission.Policies.Session {
			if name == policy.CreateAction {
				return policy, "create", true
			}
			if slices.Contains(policy.UseActions, name) {
				return policy, "use", true
			}
		}
	}
	return policies.SessionPolicy{}, "", false
}

func policyBatch(policy policies.SessionPolicy, batch []actions.NextAction) error {
	creators, consumers := 0, 0
	for _, action := range batch {
		if action.ActionName == policy.CreateAction {
			creators++
		} else if slices.Contains(policy.UseActions, action.ActionName) {
			consumers++
		}
	}
	if creators > 1 {
		return errors.New("session_policy_violation: multiple creations in one policy group")
	}
	if creators != 0 && consumers != 0 {
		return errors.New("session_policy_violation: create and use must be accepted separately")
	}
	return nil
}

func policyScope(operationID, creator string) string {
	sum := sha256.Sum256([]byte("commander/session-policy/" + operationID + "/" + creator))
	// A derived version-8 UUID keeps each group's scope stable without another record.
	sum[6] = sum[6]&0x0f | 0x80
	sum[8] = sum[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", sum[:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}

func policyRecord(op operations.Operation, creator, scopeID string, scopes []model.Scope) (model.Record, bool, error) {
	for i := len(op.Tasks) - 1; i >= 0; i-- {
		task := op.Tasks[i]
		intent := task.SessionIntent
		if task.ActionName != creator || intent == nil || intent.Error != "" {
			continue
		}
		if intent.Validate() != nil || intent.Mode != model.Create || intent.ScopeID != scopeID || intent.ID != task.ID {
			return model.Record{}, false, errors.New("session_policy_violation: accepted creator has a conflicting session intent")
		}
		for _, scope := range scopes {
			if scope.ID != scopeID {
				continue
			}
			record, err := scope.Find(intent.ID)
			if err != nil {
				return model.Record{}, false, errPolicyPending
			}
			if record.Creation != (model.ExecutionRef{OperationID: task.OperationID, TaskID: task.ID, CommandID: task.CommandID}) {
				return model.Record{}, false, errors.New("session_policy_violation: stored session belongs to another creation")
			}
			return record, true, nil
		}
		return model.Record{}, false, errPolicyPending
	}
	return model.Record{}, false, nil
}
