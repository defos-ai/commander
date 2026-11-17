package fakes

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/defos-ai/commander/framework/domain/agents"
)

type stoppingRecords map[string]agents.Agent

type StopSelectionInventory struct {
	records      atomic.Pointer[stoppingRecords]
	disappearID  string
	registration *agents.Agent
}

func NewStopSelectionInventory(records ...agents.Agent) *StopSelectionInventory {
	store := &StopSelectionInventory{}
	next := make(stoppingRecords)
	for _, record := range records {
		next[record.ID] = record.Clone()
	}
	store.records.Store(&next)
	return store
}

func (s *StopSelectionInventory) ConfigureStopRace(disappearID string, registration agents.Agent) {
	s.disappearID = disappearID
	copy := registration.Clone()
	s.registration = &copy
}

func (s *StopSelectionInventory) Upsert(ctx context.Context, record agents.Agent) error {
	for ctx.Err() == nil {
		previous := s.records.Load()
		next := cloneStoppingRecords(*previous)
		copy := record.Clone()
		copy.StopCommandID, copy.StopRequestedAt = "", time.Time{}
		if stored, ok := next[record.ID]; ok {
			copy.StopCommandID, copy.StopRequestedAt = stored.StopCommandID, stored.StopRequestedAt
			if copy.StopCommandID != "" && copy.Status != agents.AgentStopped {
				copy.Status = agents.AgentStopRequested
			}
		}
		next[copy.ID] = copy
		if s.records.CompareAndSwap(previous, &next) {
			return nil
		}
	}
	return ctx.Err()
}

func (s *StopSelectionInventory) Get(ctx context.Context, agentID string) (agents.Agent, error) {
	if err := ctx.Err(); err != nil {
		return agents.Agent{}, err
	}
	if record, ok := (*s.records.Load())[agentID]; ok {
		return record.Clone(), nil
	}
	return agents.Agent{}, agents.ErrUnknownAgent
}

func (s *StopSelectionInventory) List(ctx context.Context) ([]agents.Agent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result := []agents.Agent{}
	for _, record := range *s.records.Load() {
		result = append(result, record.Clone())
	}
	return result, nil
}

func (s *StopSelectionInventory) RequestStop(ctx context.Context, agentID, commandID string, requestedAt time.Time) (agents.Agent, error) {
	if err := agents.ValidateStopRequest(commandID, requestedAt); err != nil {
		return agents.Agent{}, err
	}
	for ctx.Err() == nil {
		previous := s.records.Load()
		next := cloneStoppingRecords(*previous)
		record, ok := next[agentID]
		if !ok {
			return agents.Agent{}, agents.ErrUnknownAgent
		}
		if agentID == s.disappearID {
			delete(next, agentID)
		} else if record.StopCommandID == "" && record.Status != agents.AgentStopped {
			record.Status, record.StopCommandID, record.StopRequestedAt = agents.AgentStopRequested, commandID, requestedAt
			next[agentID] = record
		}
		if s.registration != nil {
			if _, exists := next[s.registration.ID]; !exists {
				next[s.registration.ID] = s.registration.Clone()
			}
		}
		if s.records.CompareAndSwap(previous, &next) {
			if agentID == s.disappearID {
				return agents.Agent{}, agents.ErrUnknownAgent
			}
			return record.Clone(), nil
		}
	}
	return agents.Agent{}, ctx.Err()
}

func (s *StopSelectionInventory) DeleteExpired(ctx context.Context, cutoff time.Time) ([]agents.Agent, error) {
	for ctx.Err() == nil {
		previous := s.records.Load()
		next := cloneStoppingRecords(*previous)
		removed := []agents.Agent{}
		for agentID, record := range next {
			if record.Status != agents.AgentStopRequested && !record.LastHeartbeat.After(cutoff) {
				delete(next, agentID)
				removed = append(removed, record.Clone())
			}
		}
		if s.records.CompareAndSwap(previous, &next) {
			return removed, nil
		}
	}
	return nil, ctx.Err()
}

func cloneStoppingRecords(records stoppingRecords) stoppingRecords {
	result := make(stoppingRecords, len(records))
	for agentID, record := range records {
		result[agentID] = record.Clone()
	}
	return result
}
