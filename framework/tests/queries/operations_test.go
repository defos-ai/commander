package queries_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/queries"
)

func TestTaskQueriesRetainSavedWorkWhenObservationsFail(t *testing.T) {
	saved := operations.Task{ID: "task", OperationID: "operation", Status: operations.TaskRunning}
	unavailable := errors.New("observation unavailable")
	for _, source := range []string{"inventory", "parent status"} {
		for _, canceled := range []bool{false, true} {
			name := source
			if canceled {
				name += " with cancellation"
			}
			t.Run(name, func(t *testing.T) {
				for _, paginated := range []bool{false, true} {
					ctx, cancel := context.WithCancel(t.Context())
					defer cancel()
					failObservation := func() {
						if canceled {
							cancel()
						}
					}
					store := operationStore{
						get: func(context.Context, string) (operations.Operation, error) {
							if source == "parent status" {
								failObservation()
								return operations.Operation{}, unavailable
							}
							return operations.Operation{ID: saved.OperationID, Status: operations.Running}, nil
						},
						getTask: func(context.Context, string, string) (operations.Task, error) { return saved.Clone(), nil },
						listTasks: func(context.Context, string, string, int) ([]operations.Task, error) {
							return []operations.Task{saved.Clone()}, nil
						},
					}
					q := queries.Operations{Store: store, Agents: agentStore{list: func(context.Context) ([]agents.Agent, error) {
						if source == "parent status" {
							t.Fatal("queried inventory without observing the parent status")
						}
						failObservation()
						return nil, unavailable
					}}}
					var items []queries.TaskView
					var err error
					if paginated {
						items, err = q.ListTasks(ctx, saved.OperationID, "", 10)
					} else {
						var item queries.TaskView
						item, err = q.GetTask(ctx, saved.OperationID, saved.ID)
						items = []queries.TaskView{item}
					}
					if canceled {
						if !errors.Is(err, context.Canceled) {
							t.Fatalf("query swallowed caller cancellation: %v", err)
						}
						continue
					}
					if err != nil || len(items) != 1 || !reflect.DeepEqual(items[0].Task, saved) ||
						items[0].AgentType != "" || len(items[0].MissingRequirements) != 0 {
						t.Fatalf("optional observation failure lost saved work or invented a wait: %+v, %v", items, err)
					}
				}
			})
		}
	}
}

func TestOperationQueriesPropagatePrimaryReadFailures(t *testing.T) {
	failure := errors.New("saved work unavailable")
	q := queries.Operations{Store: operationStore{
		get:       func(context.Context, string) (operations.Operation, error) { return operations.Operation{}, failure },
		list:      func(context.Context) ([]operations.Operation, error) { return nil, failure },
		getTask:   func(context.Context, string, string) (operations.Task, error) { return operations.Task{}, failure },
		listTasks: func(context.Context, string, string, int) ([]operations.Task, error) { return nil, failure },
	}, Agents: agentStore{list: func(context.Context) ([]agents.Agent, error) {
		t.Fatal("queried inventory after a primary read failure")
		return nil, nil
	}}}
	for name, read := range map[string]func() error{
		"operation":  func() error { _, err := q.Get(t.Context(), "operation"); return err },
		"operations": func() error { _, err := q.List(t.Context(), true); return err },
		"task":       func() error { _, err := q.GetTask(t.Context(), "operation", "task"); return err },
		"tasks":      func() error { _, err := q.ListTasks(t.Context(), "operation", "", 10); return err },
	} {
		t.Run(name, func(t *testing.T) {
			if err := read(); !errors.Is(err, failure) {
				t.Fatalf("primary read failure was swallowed: %v", err)
			}
		})
	}
}

func TestCompactOperationListingDoesNotDependOnInventory(t *testing.T) {
	saved := []operations.Operation{{ID: "second", Status: operations.Running}, {ID: "first", Status: operations.Waiting}}
	q := queries.Operations{Store: operationStore{list: func(context.Context) ([]operations.Operation, error) {
		return []operations.Operation{saved[0].Clone(), saved[1].Clone()}, nil
	}}, Agents: agentStore{list: func(context.Context) ([]agents.Agent, error) {
		t.Fatal("compact listing queried optional inventory")
		return nil, nil
	}}}
	items, err := q.List(t.Context(), false)
	if err != nil || len(items) != 2 || items[0].ID != saved[0].ID || items[1].ID != saved[1].ID {
		t.Fatalf("compact listing changed store order or failed without inventory: %+v, %v", items, err)
	}
}

func TestEventWaitRereadsAfterObservingSettlement(t *testing.T) {
	event := operations.Event{Sequence: 8}
	settled := false
	q := queries.Operations{Store: operationStore{
		get: func(context.Context, string) (operations.Operation, error) {
			// The terminal transition committed between reading events and status.
			settled = true
			return operations.Operation{ID: "operation", Status: operations.Completed}, nil
		},
		listEvents: func(_ context.Context, operationID string, after uint64, limit int) ([]operations.Event, error) {
			if operationID != "operation" || after != 7 || limit != 1 {
				t.Fatal("event wait changed the requested page")
			}
			if settled {
				return []operations.Event{event}, nil
			}
			return nil, nil
		},
	}}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	items, err := q.ListEvents(ctx, "operation", 7, 1, true)
	if err != nil || !reflect.DeepEqual(items, []operations.Event{event}) {
		t.Fatalf("settlement hid an event committed during the query: %+v, %v", items, err)
	}
}

type operationStore struct {
	operations.OperationStore
	get        func(context.Context, string) (operations.Operation, error)
	list       func(context.Context) ([]operations.Operation, error)
	getTask    func(context.Context, string, string) (operations.Task, error)
	listTasks  func(context.Context, string, string, int) ([]operations.Task, error)
	listEvents func(context.Context, string, uint64, int) ([]operations.Event, error)
}

func (s operationStore) Get(ctx context.Context, operationID string) (operations.Operation, error) {
	return s.get(ctx, operationID)
}

func (s operationStore) List(ctx context.Context) ([]operations.Operation, error) {
	return s.list(ctx)
}

func (s operationStore) GetTask(ctx context.Context, operationID, taskID string) (operations.Task, error) {
	return s.getTask(ctx, operationID, taskID)
}

func (s operationStore) ListTasks(ctx context.Context, operationID, after string, limit int) ([]operations.Task, error) {
	return s.listTasks(ctx, operationID, after, limit)
}

func (s operationStore) ListEvents(ctx context.Context, operationID string, after uint64, limit int) ([]operations.Event, error) {
	return s.listEvents(ctx, operationID, after, limit)
}

type agentStore struct {
	agents.AgentStore
	list func(context.Context) ([]agents.Agent, error)
}

func (s agentStore) List(ctx context.Context) ([]agents.Agent, error) {
	return s.list(ctx)
}
