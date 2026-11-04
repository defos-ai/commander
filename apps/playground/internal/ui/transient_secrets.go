package ui

import (
	"context"
	"errors"
	"sort"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/defos-ai/commander/apps/admin/client"
	adminsecrets "github.com/defos-ai/commander/apps/admin/secrets"
	bridge "github.com/defos-ai/commander/apps/playground/internal/mcp"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/host"
	"github.com/defos-ai/commander/framework/id"
)

const secretPollInterval = 2 * time.Second

type transientKey struct {
	connection Connection[host.BasicCredentials]
	requestID  string
}

type secretWork struct {
	cancel     context.CancelFunc
	done       <-chan struct{}
	connection Connection[host.BasicCredentials]
}

type transientAttempt struct {
	expiresAt time.Time
	accepted  bool
}

type transientSecrets struct {
	requests       []adminsecrets.TransientRequest
	reusable       []adminsecrets.Request
	source         Connection[host.BasicCredentials]
	discovery      *secretWork
	nextScan       time.Time
	loginDismissed bool
	scanManual     bool

	current   *adminsecrets.TransientRequest
	detail    *secretWork
	nextRead  time.Time
	monitor   bool
	dismissed map[transientKey]time.Time
	attempted map[transientKey]transientAttempt
	work      map[*secretWork]bool
}

type transientResult struct {
	work      *secretWork
	kind      string
	requestID string
	requests  []adminsecrets.TransientRequest
	reusable  []adminsecrets.Request
	request   *adminsecrets.TransientRequest
	err       error
}

func liveSecretTask(operation bridge.Operation, task bridge.Task) bool {
	return !operation.Terminal() && task.Status == "running" && task.DeadlineAt != nil &&
		time.Now().Before(*task.DeadlineAt) && id.Valid(task.ID) && id.Valid(task.CommandID) && id.ValidAgent(task.AgentID)
}

func taskNeedsSecret(task bridge.Task, name string) bool {
	for _, requirement := range task.MissingRequirements {
		if requirement.Kind == actions.SecretRequirement && requirement.Name == name {
			return true
		}
	}
	return false
}

func (m *Model) matchesTransient(request adminsecrets.TransientRequest, requireMissing bool) bool {
	operation, exists := m.operations[request.OperationID]
	if !exists {
		return false
	}
	for _, task := range operation.Tasks {
		if liveSecretTask(operation, task) && task.ID == request.TaskID && task.CommandID == request.CommandID &&
			task.AgentID == request.AgentID && (task.AgentType == "" || task.AgentType == request.AgentType) {
			return !requireMissing || task.AgentType == request.AgentType && taskNeedsSecret(task, request.Name)
		}
	}
	return false
}

func (m *Model) hasRuntimeSecrets() bool {
	for _, operation := range m.operations {
		for _, task := range operation.Tasks {
			if liveSecretTask(operation, task) {
				for _, requirement := range task.MissingRequirements {
					if requirement.Kind == actions.SecretRequirement {
						return true
					}
				}
			}
		}
	}
	return false
}

func (m *Model) startSecretWork(connection Connection[host.BasicCredentials], run func(context.Context, *client.Client) transientResult) (*secretWork, tea.Cmd) {
	ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
	done := make(chan struct{})
	work := &secretWork{cancel: cancel, done: done, connection: connection}
	if m.transient.work == nil {
		m.transient.work = make(map[*secretWork]bool)
	}
	m.transient.work[work] = true
	results := make(chan transientResult, 1)
	go func() {
		defer close(done)
		defer cancel()
		api, err := client.New(client.Connection{URL: connection.URL, Credentials: connection.Credentials})
		result := transientResult{err: err}
		if err == nil {
			defer api.Close()
			result = run(ctx, api)
		}
		result.work = work
		results <- result
	}()
	return work, func() tea.Msg { return <-results }
}

func (m *Model) reapSecretWork() {
	for work := range m.transient.work {
		select {
		case <-work.done:
			delete(m.transient.work, work)
		default:
		}
	}
	for key, expiry := range m.transient.dismissed {
		if !time.Now().Before(expiry) {
			delete(m.transient.dismissed, key)
		}
	}
	for key, attempt := range m.transient.attempted {
		if !time.Now().Before(attempt.expiresAt) {
			delete(m.transient.attempted, key)
		}
	}
	if m.transient.source != m.config.API {
		m.transient.requests, m.transient.reusable = nil, nil
		m.transient.nextScan = time.Time{}
		if m.transient.discovery != nil {
			m.transient.discovery.cancel()
			m.transient.discovery = nil
		}
		m.transient.source = m.config.API
	}
}

func (m *Model) closeSecretWork() {
	for work := range m.transient.work {
		work.cancel()
	}
	for work := range m.transient.work {
		<-work.done
	}
}

func (m *Model) discoverSecrets(manual bool) tea.Cmd {
	state := &m.transient
	if state.discovery != nil || !manual && (time.Now().Before(state.nextScan) || state.loginDismissed) {
		return nil
	}
	if m.config.API.URL == "" || m.config.API.Credentials.Password == "" {
		if !manual && state.loginDismissed {
			return nil
		}
		state.loginDismissed = true
		state.nextScan = time.Now().Add(secretPollInterval)
		return m.connectAdminAPI("Connect to the API to check secret requests.", func() tea.Cmd { return m.discoverSecrets(true) })
	}
	state.source = m.config.API
	state.loginDismissed = false
	state.scanManual = manual
	state.nextScan = time.Now().Add(secretPollInterval)
	var command tea.Cmd
	state.discovery, command = m.startSecretWork(m.config.API, func(ctx context.Context, api *client.Client) transientResult {
		result := transientResult{kind: "discovery"}
		result.requests, result.err = adminsecrets.TransientRequests(ctx, api, "")
		if result.err == nil {
			result.reusable, result.err = adminsecrets.Requests(ctx, api, "")
		}
		return result
	})
	return command
}

func (m *Model) openTransientSecret(manual bool) (tea.Cmd, bool) {
	state := &m.transient
	if state.source != m.config.API {
		return nil, false
	}
	for _, request := range state.requests {
		key := transientKey{m.config.API, request.ID}
		if request.Status != "pending" || !time.Now().Before(request.ExpiresAt) || !m.matchesTransient(request, true) || !manual && !state.dismissed[key].IsZero() {
			continue
		}
		if state.dismissed == nil {
			state.dismissed = make(map[transientKey]time.Time)
		}
		state.dismissed[key] = request.ExpiresAt
		m.beginSecretPanel(nil, false)
		state.current, state.monitor = &request, true
		m.secrets.stage, m.secrets.message = secretLoading, "Checking one-time request…"
		return m.readTransientSecret(), true
	}
	return nil, false
}

func (m *Model) resetTransientPanel() {
	state := &m.transient
	if state.detail != nil {
		state.detail.cancel()
		state.detail = nil
	}
	state.current = nil
	state.monitor = false
}

func (m *Model) syncTransientSecret() tea.Cmd {
	state := &m.transient
	request := state.current
	if m.secrets.connection != m.config.API || !time.Now().Before(request.ExpiresAt) || !m.matchesTransient(*request, false) {
		if state.monitor {
			if state.detail != nil {
				state.detail.cancel()
				state.detail = nil
			}
			state.monitor = false
			m.finishSecrets("This request expired or its execution is no longer active. Check the operation before requesting another value.")
		}
		return nil
	}
	if state.monitor && state.detail == nil && !time.Now().Before(state.nextRead) {
		return m.readTransientSecret()
	}
	return nil
}

func (m *Model) readTransientSecret() tea.Cmd {
	state := &m.transient
	if state.current == nil || state.detail != nil || m.secrets.stage == secretSaving {
		return nil
	}
	requestID := state.current.ID
	state.nextRead = time.Now().Add(secretPollInterval)
	var command tea.Cmd
	state.detail, command = m.startSecretWork(m.secrets.connection, func(ctx context.Context, api *client.Client) transientResult {
		request, err := adminsecrets.GetTransientRequest(ctx, api, requestID)
		return transientResult{kind: "detail", requestID: requestID, request: request, err: err}
	})
	return command
}

func (m *Model) sendTransientSecret() tea.Cmd {
	state := &m.transient
	request := state.current
	key := transientKey{m.secrets.connection, request.ID}
	if m.secrets.stage != secretValues || len(m.secrets.fields) != 1 || !state.attempted[key].expiresAt.IsZero() {
		return nil
	}
	if !time.Now().Before(request.ExpiresAt) || !m.matchesTransient(*request, false) || m.secrets.connection != m.config.API {
		return m.syncTransientSecret()
	}
	value := m.secrets.fields[0].input.Value()
	if !utf8.ValidString(value) || len(value) > 64<<10 {
		m.secrets.message = "The value must be valid UTF-8 and at most 64 KiB."
		return nil
	}
	if state.attempted == nil {
		state.attempted = make(map[transientKey]transientAttempt)
	}
	state.attempted[key] = transientAttempt{expiresAt: request.ExpiresAt}
	if state.detail != nil {
		state.detail.cancel()
	}
	m.clearSecretInputs()
	m.secrets.stage, m.secrets.message = secretSaving, "Sending one-time value…"
	var command tea.Cmd
	state.detail, command = m.startSecretWork(m.secrets.connection, func(ctx context.Context, api *client.Client) transientResult {
		defer func() { value = "" }()
		err := adminsecrets.ProvideTransient(ctx, api, request.ID, value)
		return transientResult{kind: "submission", requestID: request.ID, err: err}
	})
	return command
}

func (m *Model) transientResult(result transientResult) tea.Cmd {
	state := &m.transient
	if result.work == state.discovery {
		state.discovery = nil
		if result.work.connection != m.config.API || m.stage != stageChat {
			return nil
		}
		if result.err != nil {
			if (result.kind == "" || errors.Is(result.err, client.ErrAuthentication)) && !m.secrets.open {
				m.config.API.Credentials.Password = ""
				state.loginDismissed = true
				return m.connectAdminAPI("API login is needed to check secret requests.", func() tea.Cmd { return m.discoverSecrets(true) })
			}
			if state.scanManual {
				m.add("notice", "Could not check secret requests. Check the API connection and use /secrets to try again.")
			}
			return nil
		}
		state.requests, state.reusable = result.requests, result.reusable
		sort.Slice(state.requests, func(i, j int) bool { return state.requests[i].RequestedAt.Before(state.requests[j].RequestedAt) })
		if !m.secrets.open {
			return m.openSecrets(false)
		}
		return nil
	}
	if result.work != state.detail || !m.secrets.open || state.current == nil || result.work.connection != m.config.API || result.err == nil && result.requestID != state.current.ID {
		return nil
	}
	state.detail = nil
	if result.kind == "submission" {
		key := transientKey{result.work.connection, result.requestID}
		attempt := state.attempted[key]
		attempt.accepted = result.err == nil
		state.attempted[key] = attempt
		message := "Awaiting receipt. Checking whether the agent received the value."
		if result.err != nil {
			message = "Submission outcome is uncertain. Checking status; the value will not be resent."
		}
		m.finishSecrets(message)
		return m.readTransientSecret()
	}
	if result.err != nil || result.request == nil || !sameTransientRequest(*state.current, *result.request) {
		m.finishSecrets("Could not verify this request. Use R to check status or open Admin Secrets. No value will be resent.")
		return nil
	}
	state.current = result.request
	request := result.request
	if request.Status != "pending" || !time.Now().Before(request.ExpiresAt) {
		state.monitor = false
		message := "This one-time request is closed."
		if request.Status == "fulfilled" {
			message = "Received by agent. This does not confirm that a website accepted it."
		} else if request.Status == "expired" || !time.Now().Before(request.ExpiresAt) {
			message = "This one-time request expired. Let the agent decide whether another value is needed."
		}
		m.finishSecrets(message)
		return nil
	}
	if !m.matchesTransient(*request, false) {
		return m.syncTransientSecret()
	}
	key := transientKey{m.secrets.connection, request.ID}
	if attempt := state.attempted[key]; !attempt.expiresAt.IsZero() {
		message := "Submission outcome is uncertain. The request is still pending; the value will not be resent."
		if attempt.accepted {
			message = "Awaiting receipt. The request is still pending; the value will not be resent."
		}
		m.finishSecrets(message)
		return nil
	}
	if m.secrets.stage != secretValues {
		m.clearSecretInputs()
		field := m.newSecretField(request.Name, "", true)
		field.input.CharLimit = 64 << 10
		m.secrets.fields = []secretField{field}
		m.secrets.stage, m.secrets.message = secretValues, ""
		m.focusSecret(0)
		return m.secretInputCommand(textinput.Blink)
	}
	return nil
}

func sameTransientRequest(a, b adminsecrets.TransientRequest) bool {
	return a.ID == b.ID && a.Name == b.Name && a.AgentID == b.AgentID && a.AgentType == b.AgentType && a.OperationID == b.OperationID &&
		a.TaskID == b.TaskID && a.CommandID == b.CommandID && a.RequestedAt.Equal(b.RequestedAt) && a.ExpiresAt.Equal(b.ExpiresAt)
}
