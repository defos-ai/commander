package ui

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/defos-ai/commander/apps/admin/client"
	adminsecrets "github.com/defos-ai/commander/apps/admin/secrets"
	"github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/host"
)

type secretStage int

const (
	secretConnect secretStage = iota
	secretLoading
	secretValues
	secretSaving
	secretFinished
)

type secretRequirement struct {
	operation string
	key       secrets.Key
}

type secretField struct {
	label    string
	input    textinput.Model
	key      secrets.Key
	metadata *adminsecrets.Metadata
}

type secretPanel struct {
	open       bool
	manual     bool
	stage      secretStage
	fields     []secretField
	focused    int
	keys       []secrets.Key
	connection Connection[host.BasicCredentials]
	message    string
	dismissed  map[secretRequirement]bool
	generation uint64
	inputEpoch uint64
	cancel     context.CancelFunc
	onConnect  func() tea.Cmd
}

// Clipboard reads and cursor commands may complete after a field loses focus.
// Keep their results inside the form that initiated them, including after Esc.
type secretInputResult struct {
	generation, epoch uint64
	field             int
	message           tea.Msg
}

func (result secretInputResult) wrap(command tea.Cmd) tea.Cmd {
	if command == nil {
		return nil
	}
	return func() tea.Msg {
		result.message = command()
		return result
	}
}

func (m *Model) secretInputCommand(command tea.Cmd) tea.Cmd {
	panel := &m.secrets
	return (secretInputResult{generation: panel.generation, epoch: panel.inputEpoch, field: panel.focused}).wrap(command)
}

type secretResult struct {
	generation uint64
	metadata   []*adminsecrets.Metadata
	saved      int
	write      bool
	err        error
}

func (m *Model) pendingSecrets() map[secretRequirement]bool {
	pending := make(map[secretRequirement]bool)
	for id, operation := range m.operations {
		for _, key := range operation.MissingSecrets() {
			pending[secretRequirement{id, key}] = true
		}
	}
	return pending
}

func (m *Model) syncSecrets() tea.Cmd {
	if m.admin.running && !m.secrets.open {
		return nil
	}
	if m.stage != stageChat {
		return nil
	}
	pending := m.pendingSecrets()
	for requirement := range m.secrets.dismissed {
		if !pending[requirement] {
			delete(m.secrets.dismissed, requirement)
		}
	}
	if m.secrets.open {
		if m.secrets.manual || m.secrets.stage == secretFinished || m.secrets.stage == secretSaving {
			return nil
		}
		current := make(map[secrets.Key]bool)
		for requirement := range pending {
			current[requirement.key] = true
		}
		for _, key := range m.secrets.keys {
			if !current[key] {
				m.finishSecrets("Secret requirements changed. Use /secrets to check again.")
				if len(current) == 0 {
					m.secrets.message = "These secrets are no longer required."
				}
				break
			}
		}
		return nil
	}
	return m.openSecrets(false)
}

func (m *Model) openSecrets(manual bool) tea.Cmd {
	pending := m.pendingSecrets()
	keys := make(map[secrets.Key]bool)
	for requirement := range pending {
		if manual || !m.secrets.dismissed[requirement] {
			keys[requirement.key] = true
		}
	}
	if len(keys) == 0 {
		if manual {
			m.add("notice", "No secrets are needed by the watched operations.")
		}
		return nil
	}
	panel := &m.secrets
	if panel.dismissed == nil {
		panel.dismissed = make(map[secretRequirement]bool)
	}
	for requirement := range pending {
		if keys[requirement.key] {
			panel.dismissed[requirement] = true
		}
	}
	var ordered []secrets.Key
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Slice(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		if a.Scope.AgentType != b.Scope.AgentType {
			return a.Scope.AgentType < b.Scope.AgentType
		}
		return a.Name < b.Name
	})
	m.beginSecretPanel(ordered, false)
	if panel.connection.URL == "" || panel.connection.Credentials.Password == "" {
		m.connectSecrets("")
		return m.secretInputCommand(textinput.Blink)
	}
	return m.loadSecrets()
}

func (m *Model) openAdminSecret(key secrets.Key) tea.Cmd {
	if err := key.Validate(); err != nil {
		m.add("error", err.Error())
		return nil
	}
	m.beginSecretPanel([]secrets.Key{key}, true)
	if m.secrets.connection.URL == "" || m.secrets.connection.Credentials.Password == "" {
		m.connectSecrets("")
		return m.secretInputCommand(textinput.Blink)
	}
	return m.loadSecrets()
}

func (m *Model) connectAdminAPI(message string, resume func() tea.Cmd) tea.Cmd {
	m.beginSecretPanel(nil, true)
	if message == client.ErrAuthentication.Error() {
		m.secrets.connection.Credentials.Password = ""
	}
	m.secrets.onConnect = resume
	m.connectSecrets(message)
	return m.secretInputCommand(textinput.Blink)
}

func (m *Model) beginSecretPanel(keys []secrets.Key, manual bool) {
	m.finishSecrets("")
	panel := &m.secrets
	panel.open, panel.manual, panel.keys = true, manual, keys
	panel.connection = m.config.API
	if panel.connection.Credentials.Username == "" {
		panel.connection.Credentials.Username = "commander"
	}
	m.input.Blur()
	m.questions.input.Blur()
}

func (m *Model) newSecretField(label, value string, masked bool) secretField {
	input := textinput.New()
	input.Prompt = "› "
	input.Width = max(12, m.width-8)
	input.CharLimit = 16000
	input.PromptStyle = m.input.PromptStyle
	input.TextStyle = m.input.TextStyle
	input.PlaceholderStyle = m.input.PlaceholderStyle
	input.Cursor.Style = m.input.Cursor.Style
	input.Cursor.TextStyle = m.input.Cursor.TextStyle
	if masked {
		input.EchoMode = textinput.EchoPassword
		input.EchoCharacter = '•'
	}
	input.SetValue(value)
	return secretField{label: label, input: input}
}

func (m *Model) connectSecrets(message string) {
	m.clearSecretInputs()
	panel := &m.secrets
	panel.stage, panel.message = secretConnect, message
	panel.fields = []secretField{
		m.newSecretField("API URL", panel.connection.URL, false),
		m.newSecretField("Username", panel.connection.Credentials.Username, false),
		m.newSecretField("API password", panel.connection.Credentials.Password, true),
	}
	m.focusSecret(0)
	for i := range panel.fields {
		if panel.fields[i].input.Value() == "" {
			m.focusSecret(i)
			break
		}
	}
}

func (m *Model) focusSecret(index int) {
	panel := &m.secrets
	panel.inputEpoch++
	panel.focused = index
	for i := range panel.fields {
		if i == index {
			panel.fields[i].input.Focus()
		} else {
			panel.fields[i].input.Blur()
		}
	}
}

func (m *Model) secretContext() (context.Context, uint64) {
	if m.secrets.cancel != nil {
		m.secrets.cancel()
	}
	m.secrets.generation++
	ctx, cancel := context.WithCancel(m.ctx)
	m.secrets.cancel = cancel
	return ctx, m.secrets.generation
}

func (m *Model) loadSecrets() tea.Cmd {
	panel := &m.secrets
	api, err := client.New(client.Connection{URL: panel.connection.URL, Credentials: panel.connection.Credentials})
	if err != nil {
		m.connectSecrets(err.Error())
		return m.secretInputCommand(textinput.Blink)
	}
	m.clearSecretInputs()
	panel.stage, panel.message = secretLoading, "Checking secrets…"
	ctx, generation := m.secretContext()
	keys := append([]secrets.Key(nil), panel.keys...)
	return func() tea.Msg {
		defer api.Close()
		result := secretResult{generation: generation}
		for _, key := range keys {
			metadata, err := adminsecrets.Get(ctx, api, key)
			if err != nil {
				result.err = err
				break
			}
			result.metadata = append(result.metadata, metadata)
		}
		return result
	}
}

func (m *Model) secretResult(message tea.Msg) (tea.Cmd, bool) {
	if result, ok := message.(secretInputResult); ok {
		panel := &m.secrets
		if !panel.open || result.generation != panel.generation || result.epoch != panel.inputEpoch || result.field != panel.focused || result.field >= len(panel.fields) {
			return nil, true
		}
		if commands, ok := result.message.(tea.BatchMsg); ok {
			wrapped := make([]tea.Cmd, len(commands))
			for i, command := range commands {
				wrapped[i] = result.wrap(command)
			}
			return tea.Batch(wrapped...), true
		}
		field := &panel.fields[result.field]
		var command tea.Cmd
		field.input, command = field.input.Update(result.message)
		return result.wrap(command), true
	}
	result, ok := message.(secretResult)
	if !ok {
		return nil, false
	}
	panel := &m.secrets
	if !panel.open || result.generation != panel.generation {
		return nil, true
	}
	if result.write {
		text := "Secrets saved. Waiting for the agent."
		if panel.manual {
			text = "Secret saved."
		}
		if result.err != nil {
			text = result.err.Error() + ". Reopen /secrets to check before trying again."
			if panel.manual {
				text = result.err.Error() + ". Check saved state before trying again."
			}
			if result.saved > 0 {
				text = fmt.Sprintf("%d saved. ", result.saved) + text
			}
		}
		m.finishSecrets(text)
		return nil, true
	}
	if result.err != nil {
		if errors.Is(result.err, client.ErrAuthentication) {
			panel.connection.Credentials.Password = ""
		}
		m.connectSecrets(result.err.Error())
		return m.secretInputCommand(textinput.Blink), true
	}
	// The configured API login stays local to this UI; it is never chat context.
	m.config.API = panel.connection
	panel.stage, panel.message = secretValues, ""
	for i, metadata := range result.metadata {
		if metadata != nil && metadata.Status == "available" && !panel.manual {
			continue
		}
		key := panel.keys[i]
		field := m.newSecretField(key.Scope.AgentType+" · "+key.Name, "", true)
		field.key, field.metadata = key, metadata
		panel.fields = append(panel.fields, field)
	}
	if len(panel.fields) == 0 {
		m.finishSecrets("Secrets are saved. Waiting for the agent.")
		return nil, true
	}
	m.focusSecret(0)
	return m.secretInputCommand(textinput.Blink), true
}

func (m *Model) saveSecrets() tea.Cmd {
	panel := &m.secrets
	api, err := client.New(client.Connection{URL: panel.connection.URL, Credentials: panel.connection.Credentials})
	if err != nil {
		m.finishSecrets(err.Error())
		return nil
	}
	type write struct {
		key      secrets.Key
		value    string
		metadata *adminsecrets.Metadata
	}
	writes := make([]write, len(panel.fields))
	for i, field := range panel.fields {
		writes[i] = write{field.key, field.input.Value(), field.metadata}
	}
	m.clearSecretInputs()
	panel.stage, panel.message = secretSaving, "Saving secrets…"
	ctx, generation := m.secretContext()
	return func() tea.Msg {
		defer api.Close()
		defer clear(writes)
		result := secretResult{generation: generation, write: true}
		for i, value := range writes {
			if _, result.err = adminsecrets.Save(ctx, api, value.key, value.value, value.metadata); result.err != nil {
				break
			}
			writes[i].value = ""
			result.saved++
		}
		return result
	}
}

func (m *Model) clearSecretInputs() {
	m.secrets.inputEpoch++
	for i := range m.secrets.fields {
		m.secrets.fields[i].input.Reset()
		m.secrets.fields[i].input.Blur()
	}
	m.secrets.fields = nil
}

func (m *Model) finishSecrets(message string) {
	m.clearSecretInputs()
	m.secrets.onConnect = nil
	if m.secrets.cancel != nil {
		m.secrets.cancel()
	}
	m.secrets.generation++
	m.secrets.stage, m.secrets.message = secretFinished, message
	// Keep an explicit dismissal screen so queued typing cannot become chat text.
}

func (m *Model) dismissSecrets() {
	if m.secrets.stage == secretSaving {
		message := "Save interrupted. Use /secrets to check before trying again."
		if m.secrets.manual {
			message = "Save interrupted. Check saved state before trying again."
		}
		m.add("notice", message)
	}
	m.finishSecrets("")
	m.secrets.open = false
	m.secrets.connection = Connection[host.BasicCredentials]{}
	m.focusQuestion(m.questions.focused)
	m.refresh()
}

func (m *Model) secretKey(key tea.KeyMsg) tea.Cmd {
	panel := &m.secrets
	switch key.String() {
	case "ctrl+d":
		return tea.Quit
	case "esc", "ctrl+c":
		m.dismissSecrets()
		return textinput.Blink
	case "enter":
		if panel.stage == secretFinished {
			m.dismissSecrets()
			return textinput.Blink
		}
		if len(panel.fields) == 0 {
			return nil
		}
		if panel.focused < len(panel.fields)-1 {
			m.focusSecret(panel.focused + 1)
			return m.secretInputCommand(textinput.Blink)
		}
		if panel.stage == secretConnect {
			panel.connection = Connection[host.BasicCredentials]{
				URL: strings.TrimSpace(panel.fields[0].input.Value()),
				Credentials: host.BasicCredentials{
					Username: panel.fields[1].input.Value(),
					Password: panel.fields[2].input.Value(),
				},
			}
			if panel.onConnect != nil {
				api, err := client.New(client.Connection{URL: panel.connection.URL, Credentials: panel.connection.Credentials})
				if err != nil {
					m.connectSecrets(err.Error())
					return m.secretInputCommand(textinput.Blink)
				}
				api.Close()
				m.config.API = panel.connection
				resume := panel.onConnect
				m.dismissSecrets()
				return resume()
			}
			return m.loadSecrets()
		}
		return m.saveSecrets()
	case "tab", "shift+tab":
		if count := len(panel.fields); count > 0 {
			direction := 1
			if key.String() == "shift+tab" {
				direction = -1
			}
			m.focusSecret((panel.focused + direction + count) % count)
			return m.secretInputCommand(textinput.Blink)
		}
	}
	if len(panel.fields) == 0 {
		return nil
	}
	field := &panel.fields[panel.focused]
	var cmd tea.Cmd
	field.input, cmd = field.input.Update(key)
	return m.secretInputCommand(cmd)
}

func (m *Model) secretView() string {
	panel := &m.secrets
	title, description := "Agent secrets", "Sent directly to the API, never to the model."
	if panel.stage == secretConnect {
		title, description = "Connect to API", "API login · Kept in memory for this session."
	}
	lines := []string{m.color(title, ReticleGreen), description, ""}
	for _, field := range panel.fields {
		if field.metadata != nil && field.metadata.Status == "expired" {
			lines = append(lines, "Expired secrets are renewed without an expiry.", "")
			break
		}
	}
	// Show a window of fields on short terminals and keep the active one visible.
	visible := max(1, (m.height-10)/3)
	start := max(0, panel.focused-visible+1)
	end := min(len(panel.fields), start+visible)
	if start > 0 {
		lines = append(lines, "↑ More fields · Shift+Tab")
	}
	for i := start; i < end; i++ {
		field := &panel.fields[i]
		field.input.Width = max(12, m.width-8)
		lines = append(lines, clean(field.label), field.input.View(), "")
	}
	if end < len(panel.fields) {
		lines = append(lines, "↓ More fields · Tab")
	}
	if panel.message != "" {
		lines = append(lines, clean(panel.message), "")
	}
	footer := "Tab next · Enter continue/save · Esc dismiss"
	if panel.stage == secretFinished {
		footer = "Enter back to chat · Esc dismiss"
	} else if len(panel.fields) == 0 {
		footer = "Esc dismiss"
	}
	lines = append(lines, m.color(footer, MutedGray))
	return ansi.Wrap(strings.Join(lines, "\n"), max(20, m.width-4), "")
}
