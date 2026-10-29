package fakes

import (
	"bytes"
	"context"

	"github.com/defos-ai/commander/apps/playground/internal/llm"
	"github.com/defos-ai/commander/apps/playground/internal/ui"
)

type HistoryModel struct {
	requests chan []llm.Message
}

func NewHistoryModel() *HistoryModel {
	return &HistoryModel{requests: make(chan []llm.Message, 4)}
}

func (m *HistoryModel) Requests() <-chan []llm.Message { return m.requests }

func (m *HistoryModel) Complete(ctx context.Context, _ string, messages []llm.Message, _ []llm.Tool, delta func(string)) (llm.Message, error) {
	snapshot := append([]llm.Message(nil), messages...)
	for i := range snapshot {
		snapshot[i].Native = bytes.Clone(messages[i].Native)
		snapshot[i].Calls = append([]llm.Call(nil), messages[i].Calls...)
		for j := range snapshot[i].Calls {
			snapshot[i].Calls[j].Arguments = bytes.Clone(messages[i].Calls[j].Arguments)
		}
	}
	select {
	case m.requests <- snapshot:
	case <-ctx.Done():
		return llm.Message{}, ctx.Err()
	}
	delta("Continuing the saved conversation")
	return llm.Message{Role: "assistant", Text: "Continuing the saved conversation"}, nil
}

type HistoryStore struct {
	initial ui.History
	saved   ui.History
	URL     string
	err     error
}

func NewHistoryStore(initial ui.History, err error) *HistoryStore {
	return &HistoryStore{initial: initial, err: err}
}

func (s *HistoryStore) Load(string) (ui.History, error) { return s.initial, nil }

func (s *HistoryStore) Save(endpoint string, history ui.History) error {
	if s.err != nil {
		return s.err
	}
	s.saved, s.URL = history, endpoint
	return nil
}

// Snapshot is read after the UI has closed and joined its history writer.
func (s *HistoryStore) Snapshot() ui.History { return s.saved }
