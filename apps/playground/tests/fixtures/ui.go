package fixtures

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/defos-ai/commander/apps/playground/internal/ui"
	"github.com/defos-ai/commander/framework/host"
)

type UIDriver struct {
	t        *testing.T
	ctx      context.Context
	Model    *ui.Model
	messages chan tea.Msg
	closed   bool
}

func NewUIDriver(t *testing.T, ctx context.Context, model *ui.Model) *UIDriver {
	t.Helper()
	driver := &UIDriver{t: t, ctx: ctx, Model: model, messages: make(chan tea.Msg, 32)}
	t.Cleanup(driver.Close)
	return driver
}

func (u *UIDriver) Close() {
	if !u.closed {
		u.closed = true
		u.Model.Close()
	}
}

func NewUI(t *testing.T, ctx context.Context, endpoint, provider string) *UIDriver {
	t.Helper()
	return NewUIWithConfig(t, ctx, ui.Config{Provider: "other", Model: "fixture", BaseURL: provider, MCP: ui.Connection[host.BearerCredentials]{URL: endpoint}, Keys: map[string]string{"other": "test-only"}, NoColor: true})
}

func NewUIWithConfig(t *testing.T, ctx context.Context, config ui.Config) *UIDriver {
	t.Helper()
	u := NewUIDriver(t, ctx, ui.New(ctx, config, io.Discard))
	u.Model.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	u.Run(u.Model.Init())
	ready := "Commander connected"
	if config.AllInOne {
		ready = fmt.Sprintf("Commander ready · all-in-one · %d agents connected", config.LocalAgentCount)
	}
	u.Wait("connection", func(view string) bool { return strings.Contains(view, ready) })
	return u
}

func NewProviderSetup(t *testing.T, config ui.Config, output io.Writer) *UIDriver {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	u := NewUIDriver(t, ctx, ui.New(ctx, config, output))
	u.Model.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	u.Run(u.Model.Init())
	return u
}

func (u *UIDriver) Messages() <-chan tea.Msg { return u.messages }

func (u *UIDriver) Run(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	go func() {
		msg := cmd()
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, child := range batch {
				u.Run(child)
			}
			return
		}
		select {
		case u.messages <- msg:
		case <-u.ctx.Done():
		}
	}()
}

func (u *UIDriver) Key(key tea.KeyMsg) { _, cmd := u.Model.Update(key); u.Run(cmd) }
func (u *UIDriver) Text(text string)   { u.Key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(text)}) }

func (u *UIDriver) Wait(description string, check func(string) bool) {
	u.t.Helper()
	for !check(u.Model.View()) {
		select {
		case msg := <-u.messages:
			_, cmd := u.Model.Update(msg)
			u.Run(cmd)
		case <-u.ctx.Done():
			u.t.Fatalf("waiting for %s: %s", description, u.Model.View())
		}
	}
}
