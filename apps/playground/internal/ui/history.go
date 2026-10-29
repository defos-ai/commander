package ui

import (
	"errors"

	"github.com/defos-ai/commander/apps/playground/internal/chat"
	"github.com/defos-ai/commander/apps/playground/internal/llm"
)

type History struct {
	Version  int            `json:"version"`
	Provider string         `json:"provider"`
	Model    string         `json:"model"`
	BaseURL  string         `json:"base_url"`
	Messages []llm.Message  `json:"messages,omitempty"`
	Entries  []HistoryEntry `json:"entries,omitempty"`
}

type HistoryEntry struct {
	Kind   string `json:"kind"`
	Text   string `json:"text"`
	CallID string `json:"call_id,omitempty"`
}

type historyState struct {
	messages []llm.Message
	updates  chan History
	errors   chan error
	done     chan struct{}
	failed   bool
	err      error
}

func (m *Model) restoreHistory() []llm.Message {
	if m.config.LoadHistory != nil {
		saved, err := m.config.LoadHistory(m.config.MCP.URL)
		if err != nil || (saved.Version != 0 && saved.Version != 1) {
			m.history.failed = true
			m.add("notice", "Saved chat history could not be loaded. It has been left unchanged.")
			return nil
		}
		var entries []entry
		for _, item := range saved.Entries {
			switch item.Kind {
			case "user", "assistant", "success", "error", "notice", "admin":
			case "tool":
				item.Kind = "error"
				item.Text += " interrupted; inspect Commander state before retrying."
			default:
				continue
			}
			entries = append(entries, entry{kind: item.Kind, text: clean(item.Text), callID: item.CallID, dirty: true})
		}
		m.entries = append(entries, m.entries...)
		m.history.messages = saved.Messages
		if saved.Provider != m.provider.ID || saved.Model != m.settings.Model || saved.BaseURL != m.settings.BaseURL {
			m.history.messages = chat.PortableHistory(m.history.messages)
		}
		if len(saved.Entries) != 0 {
			m.add("notice", "Saved conversation restored. Use /watch <id> to resume operation review.")
		}
		m.refresh()
	}
	if m.config.SaveHistory != nil {
		m.history.updates = make(chan History, 1)
		m.history.errors = make(chan error, 1)
		m.history.done = make(chan struct{})
		updates, errors, done := m.history.updates, m.history.errors, m.history.done
		save, endpoint := m.config.SaveHistory, m.config.MCP.URL
		go func() {
			defer close(done)
			for snapshot := range updates {
				if err := save(endpoint, snapshot); err != nil {
					errors <- err
					return
				}
			}
		}()
	}
	return m.history.messages
}

func (m *Model) queueHistory() {
	m.checkHistoryError()
	if m.history.updates == nil || m.history.failed {
		return
	}
	snapshot := History{Version: 1, Provider: m.provider.ID, Model: m.settings.Model, BaseURL: m.settings.BaseURL, Messages: m.history.messages}
	for _, item := range m.entries {
		if !item.private {
			snapshot.Entries = append(snapshot.Entries, HistoryEntry{Kind: item.kind, Text: item.text, CallID: item.callID})
		}
	}
	select {
	case m.history.updates <- snapshot:
		return
	default:
	}
	select {
	case <-m.history.updates:
	default:
	}
	select {
	case m.history.updates <- snapshot:
	default:
	}
}

func (m *Model) checkHistoryError() {
	if m.history.failed || m.history.errors == nil {
		return
	}
	select {
	case <-m.history.errors:
		m.history.failed = true
		m.history.err = errors.New("chat history could not be saved; check the state directory before restarting Playground")
		m.add("notice", "Chat history could not be saved. Check the state directory before restarting Playground.")
	default:
	}
}

func (m *Model) closeHistory() error {
	if m.history.updates == nil {
		return m.history.err
	}
	m.queueHistory()
	close(m.history.updates)
	<-m.history.done
	m.checkHistoryError()
	m.history.updates = nil
	return m.history.err
}

func (m *Model) addPrivate(kind, text string) {
	m.appendEntry(kind, text, "", true)
}
