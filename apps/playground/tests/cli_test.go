package playground_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	browsercli "github.com/defos-ai/commander-browser-agent/cli"
	webcli "github.com/defos-ai/commander-web-agent/cli"
	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/apps/playground/cli"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/domain/reports"
	"github.com/defos-ai/commander/framework/id"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestAllInOne(t *testing.T) {
	t.Run("default builder and spawner startup", testAllInOneDefaultAgents)
	t.Run("shared API and MCP workflow", testAllInOneWorkflow)
	t.Run("optional Admin lifecycle", testAllInOneAdmin)
	t.Run("advertised Admin address", testAllInOneAdminAddress)
	t.Run("generated API and MCP credentials", testAllInOneGeneratedCredentials)
	t.Run("session secret encryption", testAllInOneSecrets)
	t.Run("API startup failures clean up local hosts", testAllInOneAPIFailure)
	t.Run("invalid MCP token starts no local hosts", testAllInOneMCPFailure)
}

func TestSpawnAgentsFlagRemoved(t *testing.T) {
	err := cli.Run(t.Context(), []string{"--all-in-one", "--spawn-agents=github.com/defos-ai/commander-web-agent@v0.2.0"}, strings.NewReader(""), io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "flag provided but not defined: -spawn-agents") {
		t.Fatalf("removed startup flag: got %v", err)
	}
}

func testAllInOneWorkflow(t *testing.T) {
	temporary := allInOneEnvironment(t)
	t.Setenv("PATH", t.TempDir()) // Startup and explicit web fixtures need no Git or browser.
	t.Setenv("CHROMIUM_PATH", filepath.Join(t.TempDir(), "missing-browser"))
	t.Setenv("CHROMIUM_NO_SANDBOX", "false")
	website := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `<html lang="en"><title>Release preview</title><meta name="robots" content="noindex"><h1>Preview</h1></html>`)
	}))
	defer website.Close()
	operationID := id.New()
	var requests atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		var body struct {
			Messages []struct{ Role, Content string }
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		for _, message := range body.Messages {
			if strings.Contains(message.Content, "all-in-one-mcp-secret") {
				t.Error("MCP credentials leaked into a model request")
			}
		}
		request := requests.Add(1)
		expected := map[int32][]string{
			2: {"review-website-no-browser", "review-website-with-browser"},
			3: {"starting_actions", "rules", "for_actions", "aggregate-results", "decide-next-actions", "decision_by", "input_schema", "output_schema"},
			4: {"web-agent-", "browser-agent-", "builder-agent-", "checkout-template", "build-agent", "publish-agent", "release-agent", "spawner-agent-", "spawn-agent"},
		}
		for _, want := range expected[request] {
			if len(body.Messages) == 0 || body.Messages[len(body.Messages)-1].Role != "tool" || !strings.Contains(body.Messages[len(body.Messages)-1].Content, want) {
				t.Errorf("model did not receive the real tool result containing %q", want)
			}
		}
		var delta map[string]any
		finish := "tool_calls"
		switch request {
		case 1, 3:
			name := "list_missions"
			if request == 3 {
				name = "list_agents"
			}
			delta = toolDelta(name, map[string]any{})
		case 2:
			delta = toolDelta("describe_mission", map[string]any{"mission_name": "review-website-no-browser"})
		case 4:
			delta = toolDelta("start_operation", map[string]any{"operation_id": operationID, "mission_name": "review-website-no-browser", "input": map[string]any{"url": website.URL}})
		case 5:
			delta = toolDelta("assign_task", map[string]any{"operation_id": operationID, "action_name": "discover-pages-no-browser", "input": map[string]any{"url": website.URL}})
		case 6:
			delta, finish = map[string]any{"content": "**Review running**"}, "stop"
		case 7, 9:
			// Continuations must reach the model automatically, carrying actual
			// results and then the operator's answer, without another chat prompt.
			last := body.Messages[len(body.Messages)-1]
			_, state, ok := strings.Cut(last.Content, "Current state:\n")
			var op operations.Operation
			if !ok || last.Role != "user" || json.Unmarshal([]byte(state), &op) != nil || op.ID != operationID || len(op.Tasks) != 4 || op.Preflight == nil || !op.Preflight.Ready {
				t.Errorf("model did not receive saved task evidence: %s", last.Content)
				http.Error(w, "missing operation evidence", http.StatusBadRequest)
				return
			}
			var seoFound bool
			for _, task := range op.Tasks {
				if !strings.HasPrefix(task.AgentID, "web-agent-") {
					t.Errorf("HTTP task did not execute on the web agent: %+v", task)
				}
				if task.Status != operations.TaskSucceeded || task.ActionName == "capture-page" || task.ActionName == "check-accessibility" {
					t.Errorf("unexpected browser-free task: %+v", task)
				}
				if task.ActionName == "check-seo" && task.Result != nil && bytes.Contains(task.Result.Output, []byte("Release preview")) {
					seoFound = true
				}
			}
			if !seoFound {
				t.Error("model did not receive saved SEO evidence")
			}
			if request == 7 {
				task := op.Tasks[0]
				if task.Continuation == nil || task.Continuation.Status != operations.ContinuationPending {
					t.Error("missing model continuation")
					http.Error(w, "missing continuation", http.StatusBadRequest)
					return
				}
				delta = toolDelta("ask_question", map[string]any{"operation_id": operationID, "task_id": task.ID, "version": task.Continuation.Version,
					"text": "Is the indexing restriction intentional?", "options": []string{"No, public release", "Yes, preview only"}})
			} else {
				if len(op.Questions) != 1 || op.Questions[0].Status != operations.QuestionAnswered || op.Questions[0].Answer != "Yes, preview only" {
					t.Errorf("model did not receive operator guidance: %+v", op.Questions)
				}
				delta = toolDelta("complete_operation", map[string]any{"operation_id": operationID, "output": map[string]any{"url": website.URL,
					"summary": "Release preview reviewed; operator confirmed intentional noindex", "findings": []any{},
					"limitations": []string{"Rendered accessibility and screenshots were not performed"}}})
			}
		case 8:
			delta, finish = map[string]any{"content": "Please choose how to interpret the indexing restriction."}, "stop"
		case 10, 11: // Completion can also produce a final background update.
			delta, finish = map[string]any{"content": "**Review complete**\n\n- SEO evidence checked\n- Operator answer recorded"}, "stop"
		default:
			t.Errorf("unexpected model request %d", request)
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		encoded, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", encoded)
	}))
	defer model.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	// Stop fixture agents before cancellation lets the CLI remove shared state.
	cliContext, stopCLI := context.WithCancel(context.WithoutCancel(ctx))
	stopAgents := func() {}
	input, keyboard := io.Pipe()
	var output terminalOutput
	done := make(chan error, 1)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		done <- cli.Run(cliContext, []string{"--all-in-one", "--provider=other", "--model=fixture", "--base-url=" + model.URL + "/v1", "--no-color", "--debug=false"}, input, &output, &output)
	}()
	wait := func(text string) {
		t.Helper()
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		for !strings.Contains(output.String(), text) {
			select {
			case err := <-done:
				t.Fatalf("CLI stopped before %q: %v\n%s", text, err, output.String())
			case <-tick.C:
			case <-ctx.Done():
				t.Fatalf("waiting for %q: %s", text, output.String())
			}
		}
	}
	t.Cleanup(func() {
		stopAgents()
		stopCLI()
		_ = keyboard.Close()
		_ = input.Close()
		select {
		case <-stopped:
		case <-time.After(10 * time.Second):
			t.Error("Playground did not clean up its local hosts")
		}
	})
	wait("Temporary session · results deleted on exit")
	welcome := output.String()
	for _, want := range []string{"Other (OpenAI-compatible) · fixture", "Commander ready · all-in-one · 2 agents connected", "Try: Install the web agent, then review defos.ai"} {
		if !strings.Contains(welcome, want) {
			t.Fatalf("missing startup message %q: %s", want, welcome)
		}
	}
	for _, unwanted := range []string{"http://127.0.0.1:", "API listening:", "Use /credentials", "All-in-one: 1 MCP", "Try: List missions", "Try: Run review-website-no-browser"} {
		if strings.Contains(welcome, unwanted) {
			t.Fatalf("startup still shows %q: %s", unwanted, welcome)
		}
	}
	if _, err := io.WriteString(keyboard, "/status\r"); err != nil {
		t.Fatal(err)
	}
	wait("MCP connected · watching 0 operations · 0 queued messages")
	endpoint := regexp.MustCompile(`http://127\.0\.0\.1:\d+/mcp`).FindString(output.String())
	if endpoint == "" {
		t.Fatalf("missing local endpoint: %s", output.String())
	}
	apiNotice := regexp.MustCompile(`API: (http://127\.0\.0\.1:\d+)`).FindStringSubmatch(output.String())
	if len(apiNotice) != 2 {
		t.Fatalf("missing local API endpoint: %s", output.String())
	}
	apiURL := apiNotice[1]
	adminNotice := regexp.MustCompile(`Admin:\s+(http://127\.0\.0\.1:\d+)`).FindStringSubmatch(output.String())
	if len(adminNotice) != 2 {
		t.Fatalf("missing local Admin endpoint: %s", output.String())
	}
	adminURL := adminNotice[1]
	httpClient := &http.Client{Timeout: 5 * time.Second}
	defer httpClient.CloseIdleConnections()
	get := func(path, password string, status int) []byte {
		t.Helper()
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if password != "" {
			request.SetBasicAuth("commander", password)
		}
		response, err := httpClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil || response.StatusCode != status {
			t.Fatalf("API %s: status=%d, want %d, read error=%v", path, response.StatusCode, status, err)
		}
		return body
	}
	get("/healthz", "", http.StatusOK)
	get("/readyz", "", http.StatusOK)
	var about map[string]string
	if err := json.Unmarshal(get("/aboutz", "", http.StatusOK), &about); err != nil {
		t.Fatal(err)
	}
	if len(about) != 2 || about["runtime"] != "native" || about["application"] != "commander" {
		t.Fatalf("unexpected all-in-one API metadata: %+v", about)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(endpoint, "/mcp")+"/aboutz", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := httpClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	var mcpAbout map[string]string
	err = json.NewDecoder(response.Body).Decode(&mcpAbout)
	response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK || len(mcpAbout) != 2 || mcpAbout["runtime"] != "native" || mcpAbout["application"] != "commander" {
		t.Fatalf("unexpected all-in-one MCP metadata: status=%d, metadata=%+v, error=%v", response.StatusCode, mcpAbout, err)
	}
	get("/api/v1/operations", "", http.StatusUnauthorized)
	get("/api/v1/operations", "wrong-password", http.StatusUnauthorized)
	directories, err := filepath.Glob(filepath.Join(temporary, "commander-playground-*"))
	if err != nil || len(directories) != 1 {
		t.Fatalf("locate local session: %v (%v)", directories, err)
	}
	stopAgents = startLocalWebHosts(t, directories[0])
	var agents models.ListResponse[models.AgentResponse]
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		if err := json.Unmarshal(get("/api/v1/agents", "all-in-one-api-secret", http.StatusOK), &agents); err != nil {
			t.Fatal(err)
		}
		ready := 0
		for _, agent := range agents.Items {
			if agent.Status == "ready" {
				ready++
			}
		}
		if ready == 4 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("local fixture agents did not register: %+v", agents.Items)
		case <-tick.C:
		}
	}
	if len(agents.Items) != 4 {
		t.Fatalf("unexpected local agents: %+v", agents.Items)
	}
	var webChecks, browserChecks, builderChecks, spawnerChecks int
	for _, agent := range agents.Items {
		if !regexp.MustCompile(`^(web|browser|builder|spawner)-agent-[0-9a-f]{8}$`).MatchString(agent.ID) || agent.Status != "ready" {
			t.Fatalf("unexpected bundled agent identity: %+v", agent)
		}
		for _, action := range agent.Actions {
			switch agent.Type {
			case "browser-agent":
				if (action.Name != "capture-page" && action.Name != "check-accessibility") || action.Status != "unavailable" {
					t.Fatalf("unexpected browser action without Chromium: %+v", action)
				}
				browserChecks++
			case "web-agent":
				if action.Status != "ready" || action.Name == "capture-page" || action.Name == "check-accessibility" {
					t.Fatalf("HTTP action depends on Chromium: %+v", action)
				}
				webChecks++
			case "spawner-agent":
				if action.Name != "spawn-agent" || action.Status != "ready" {
					t.Fatalf("unexpected spawner action: %+v", action)
				}
				spawnerChecks++
			case "builder-agent":
				if (action.Name != "checkout-template" && action.Name != "build-agent" && action.Name != "publish-agent" && action.Name != "release-agent") || action.Status != "unavailable" {
					t.Fatalf("unexpected builder action without tools: %+v", action)
				}
				builderChecks++
			default:
				t.Fatalf("unexpected bundled agent type: %s", agent.Type)
			}
		}
	}
	if webChecks != 5 || browserChecks != 2 || builderChecks != 4 || spawnerChecks != 1 {
		t.Fatalf("unexpected local action coverage: web=%d, browser=%d, builder=%d, spawner=%d", webChecks, browserChecks, builderChecks, spawnerChecks)
	}
	for _, token := range []string{"", "wrong-token"} {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		response, err := httpClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusUnauthorized {
			t.Errorf("MCP accepted missing or wrong token: status=%d", response.StatusCode)
		}
	}
	client, err := mcp.NewClient(&mcp.Implementation{Name: "playground-test", Version: "1"}, nil).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint, HTTPClient: &http.Client{Transport: mcpAuthTransport("all-in-one-mcp-secret")}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	tools, err := client.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools.Tools {
		switch tool.Name {
		case "clean_up_state", "run_action", "discover_actions", "stop_agent":
			t.Fatalf("non-operation command %q was exposed to Playground", tool.Name)
		}
	}

	if _, err := io.WriteString(keyboard, "Review website without a browser: "+website.URL+"\r"); err != nil {
		t.Fatal(err)
	}
	wait("Yes, preview only")
	if _, err := io.WriteString(keyboard, "\x1b[B\r"); err != nil {
		t.Fatal(err)
	}
	wait("Operator answer recorded")
	result, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "get_report", Arguments: map[string]any{"operation_id": operationID}})
	if err != nil {
		t.Fatal(err)
	}
	var raw []byte
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			raw = append(raw, text.Text...)
		}
	}
	var report reports.Report
	if result.IsError || json.Unmarshal(raw, &report) != nil || report.OperationID != operationID || !bytes.Contains(report.Output, []byte("operator confirmed intentional noindex")) {
		t.Fatalf("Playground did not persist the mission report: %s", raw)
	}
	var apiOperation models.OperationResponse
	if err := json.Unmarshal(get("/api/v1/operations/"+operationID, "all-in-one-api-secret", http.StatusOK), &apiOperation); err != nil {
		t.Fatal(err)
	}
	if apiOperation.ID != operationID || apiOperation.Status != "completed" || apiOperation.ReportID != report.ID || len(apiOperation.Tasks) != 4 {
		t.Fatalf("API did not read the MCP operation: %+v", apiOperation)
	}
	progress := apiOperation.Progress
	if progress.TotalTasks != 4 || progress.FinishedTasks != 4 || progress.PendingDecisions != 0 || progress.PendingQuestions != 0 || !progress.Settled || len(progress.TaskCounts) != 1 || progress.TaskCounts["succeeded"] != 4 {
		t.Fatalf("API did not expose settled progress: %+v", progress)
	}
	var seoFound bool
	for _, task := range apiOperation.Tasks {
		if task.ActionName == "check-seo" && task.Status == "succeeded" && task.Result != nil && bytes.Contains(task.Result.Output, []byte("Release preview")) {
			seoFound = true
		}
	}
	if !seoFound {
		t.Fatal("API did not read saved agent evidence")
	}
	var apiReport models.ReportResponse
	if err := json.Unmarshal(get("/api/v1/reports/"+report.ID, "all-in-one-api-secret", http.StatusOK), &apiReport); err != nil {
		t.Fatal(err)
	}
	if apiReport.ID != report.ID || apiReport.OperationID != operationID || !bytes.Contains(apiReport.Output, []byte("operator confirmed intentional noindex")) {
		t.Fatalf("API did not read the MCP report: %+v", apiReport)
	}
	wait("4/4 tasks finished · Review on Admin UI")
	client.Close()
	stopAgents()
	if _, err := io.WriteString(keyboard, "/exit\r"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("/exit did not stop the CLI")
	}

	if requests.Load() < 10 || strings.Contains(output.String(), "**Review complete**") || !strings.Contains(output.String(), "Review complete") {
		t.Fatalf("unexpected chat rendering: %s", output.String())
	}
	if strings.Contains(output.String(), "all-in-one-api-secret") || strings.Contains(output.String(), "all-in-one-mcp-secret") {
		t.Error("credentials leaked into Playground output")
	}
	files, err := os.ReadDir(temporary)
	if err != nil || len(files) != 0 {
		t.Fatalf("temporary state remains after exit: %v (%v)", files, err)
	}
	for _, endpoint := range []string{endpoint, apiURL, adminURL} {
		address, _ := url.Parse(endpoint)
		connection, err := net.DialTimeout("tcp", address.Host, time.Second)
		if err == nil {
			_ = connection.Close()
			t.Errorf("local host %s is still listening after /exit", endpoint)
		}
	}
}

// The workflow uses released agent hosts with local fixtures. Spawner suites and
// native/image smoke checks exercise package downloading and child ownership.
func startLocalWebHosts(t *testing.T, directory string) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.WithoutCancel(t.Context()))
	args := []string{"--bus-url=" + filepath.Join(directory, "bus"), "--storage-url=" + filepath.Join(directory, "storage")}
	done := make(chan error, 2)
	go func() { done <- webcli.Run(ctx, args, io.Discard) }()
	go func() { done <- browsercli.Run(ctx, args, io.Discard) }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			timer := time.NewTimer(10 * time.Second)
			defer timer.Stop()
			for range 2 {
				select {
				case err := <-done:
					if err != nil && !errors.Is(err, context.Canceled) {
						t.Errorf("stop workflow agent: %v", err)
					}
				case <-timer.C:
					t.Error("workflow agents did not stop before session cleanup")
					return
				}
			}
		})
	}
	t.Cleanup(stop)
	return stop
}

func allInOneEnvironment(t *testing.T) string {
	t.Helper()
	temporary := t.TempDir()
	frontend := t.TempDir()
	externalSpawner := t.TempDir()
	externalBuilder := t.TempDir()
	t.Setenv("SPAWNER_RUNTIME", "docker")
	t.Setenv("SPAWNER_DOCKER_NETWORK", "")
	t.Setenv("SPAWNER_STATE_DIR", externalSpawner)
	t.Setenv("BUILDER_WORKSPACE_DIR", externalBuilder)
	t.Cleanup(func() {
		if files, err := os.ReadDir(externalSpawner); err != nil || len(files) != 0 {
			t.Errorf("all-in-one used the external spawner state directory: %v (%v)", files, err)
		}
		if files, err := os.ReadDir(externalBuilder); err != nil || len(files) != 0 {
			t.Errorf("all-in-one used the external builder workspace directory: %v (%v)", files, err)
		}
	})
	if err := os.WriteFile(filepath.Join(frontend, "index.html"), []byte(`<html>Admin fixture</html>`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, temporary)
	}
	t.Setenv("ENV", "")
	t.Setenv("COMMANDER_LLM_API_KEY", "fixture-only")
	t.Setenv("COMMANDER_MCP_URL", "http://127.0.0.1:1/mcp") // The local host wins.
	t.Setenv("COMMANDER_MCP_TOKEN", "invalid remote token") // Local credentials win too.
	t.Setenv("COMMANDER_ADMIN_URL", "")
	t.Setenv("COMMANDER_API_URL", "invalid remote API URL") // Embedded credentials and URL win.
	t.Setenv("COMMANDER_API_USERNAME", "remote-operator")
	t.Setenv("COMMANDER_API_PASSWORD", "remote-password")
	t.Setenv("MCP_TOKEN", "all-in-one-mcp-secret")
	t.Setenv("MCP_RUNTIME", "native")
	t.Setenv("API_USERNAME", "commander")
	t.Setenv("API_PASSWORD", "all-in-one-api-secret")
	t.Setenv("API_LISTEN_ADDRESS", "127.0.0.1:0")
	t.Setenv("API_RUNTIME", "native")
	t.Setenv("SECRETS_ENCRYPTION_KEY", "")
	t.Setenv("UI_LISTEN_ADDRESS", "127.0.0.1:0")
	t.Setenv("UI_STATIC_DIR", frontend)
	t.Setenv("UI_PUBLIC_ORIGIN", "")
	t.Setenv("UI_API_UPSTREAMS", "")
	t.Setenv("PORT", "not-a-port") // All-in-one uses explicit API and Admin listeners.
	t.Setenv("API_ALLOWED_HOSTS", "localhost,127.0.0.1,::1")
	t.Setenv("API_ALLOWED_ORIGINS", "")
	t.Setenv("API_TRUSTED_PROXIES", "")
	t.Setenv("API_PROXY_HEADERS", "none")
	t.Setenv("API_SWAGGER_ENABLED", "false")
	return temporary
}

func testAllInOneGeneratedCredentials(t *testing.T) {
	var previousPassword, previousToken string
	for _, tc := range []struct {
		name, username, password, token string
	}{
		{name: "no credentials"},
		{name: "username override", username: "local-operator"},
		{name: "password and token overrides", password: "operator-supplied-secret", token: "operator-supplied-token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			temporary := allInOneEnvironment(t)
			t.Setenv("API_USERNAME", tc.username)
			t.Setenv("API_PASSWORD", tc.password)
			t.Setenv("MCP_TOKEN", tc.token)
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			input, keyboard := io.Pipe()
			defer input.Close()
			defer keyboard.Close()
			var output terminalOutput
			done := make(chan error, 1)
			stopped := make(chan struct{})
			go func() {
				defer close(stopped)
				done <- cli.Run(ctx, []string{"--all-in-one", "--provider=", "--model=", "--base-url=", "--no-color"}, input, &output, &output)
			}()
			t.Cleanup(func() {
				cancel()
				_ = keyboard.Close()
				select {
				case <-stopped:
				case <-time.After(10 * time.Second):
					t.Error("Playground did not stop local hosts")
				}
			})
			wait := func(text string) {
				t.Helper()
				tick := time.NewTicker(20 * time.Millisecond)
				defer tick.Stop()
				for !strings.Contains(output.String(), text) {
					select {
					case err := <-done:
						t.Fatalf("CLI stopped before %q: %v", text, err)
					case <-tick.C:
					case <-ctx.Done():
						t.Fatalf("timed out waiting for %q", text)
					}
				}
			}
			wait("Select your model provider")
			beforeLookup := output.String()
			if _, err := io.WriteString(keyboard, "/credentials\r"); err != nil {
				t.Fatal(err)
			}
			wait("Password:")
			wait("Token:")
			match := regexp.MustCompile(`API: (http://127\.0\.0\.1:\d+)\s+Username: ([^\r\n]+)\s+Password: ([^\r\n]+)`).FindStringSubmatch(output.String())
			if len(match) != 4 {
				t.Fatal("/credentials did not display the local API address and credentials")
			}
			apiURL, username, password := match[1], strings.TrimSpace(match[2]), strings.TrimSpace(match[3])
			mcpMatch := regexp.MustCompile(`MCP: (http://127\.0\.0\.1:\d+/mcp)\s+Token: ([^\r\n]+)`).FindStringSubmatch(output.String())
			if len(mcpMatch) != 3 {
				t.Fatal("/credentials did not display local MCP credentials")
			}
			mcpURL, token := mcpMatch[1], strings.TrimSpace(mcpMatch[2])
			if tc.token == "" {
				decoded, err := hex.DecodeString(token)
				if err != nil || len(decoded) != 32 || token == previousToken || token == password {
					t.Fatal("generated MCP token must be independent and contain 32 random bytes")
				}
				previousToken = token
			} else if token != tc.token {
				t.Error("MCP token override was not preserved")
			}
			wantUsername := tc.username
			if wantUsername == "" {
				wantUsername = "commander"
			}
			if username != wantUsername {
				t.Error("API username did not preserve the override or default to commander")
			}
			if tc.password == "" {
				decoded, err := hex.DecodeString(password)
				if err != nil || len(decoded) != 32 {
					t.Fatal("generated API password must contain 32 bytes encoded as hex")
				}
				if previousPassword == password {
					t.Error("new session reused a generated API password")
				}
				previousPassword = password
			} else if password != tc.password {
				t.Error("API password override was not preserved")
			}
			if strings.Contains(beforeLookup, password) || strings.Contains(beforeLookup, token) {
				t.Error("credentials appeared without an explicit /credentials request")
			}
			if os.Getenv("API_USERNAME") != tc.username || os.Getenv("API_PASSWORD") != tc.password || os.Getenv("MCP_TOKEN") != tc.token {
				t.Error("all-in-one changed process-wide credential environment")
			}
			mcpClient, err := mcp.NewClient(&mcp.Implementation{Name: "generated-credential-test", Version: "1"}, nil).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: mcpURL, HTTPClient: &http.Client{Transport: mcpAuthTransport(token)}}, nil)
			if err != nil {
				t.Fatalf("generated or overridden MCP credentials did not authenticate: %v", err)
			}
			mcpClient.Close()
			transport := http.DefaultTransport.(*http.Transport).Clone()
			httpClient := &http.Client{Timeout: 3 * time.Second, Transport: transport}
			defer httpClient.CloseIdleConnections()
			for _, attempt := range []struct {
				password string
				status   int
			}{
				{status: http.StatusUnauthorized},
				{password: "wrong-password", status: http.StatusUnauthorized},
				{password: password, status: http.StatusOK},
			} {
				request, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL+"/api/v1/operations", nil)
				if err != nil {
					t.Fatal(err)
				}
				if attempt.password != "" {
					request.SetBasicAuth(username, attempt.password)
				}
				response, err := httpClient.Do(request)
				if err != nil {
					t.Fatal(err)
				}
				_, readErr := io.Copy(io.Discard, response.Body)
				response.Body.Close()
				if readErr != nil {
					t.Fatal(readErr)
				}
				if response.StatusCode != attempt.status {
					t.Errorf("API credential authentication status=%d, want %d", response.StatusCode, attempt.status)
				}
			}
			// Close this fixture's unused connections before stopping API: a
			// speculative dial may still be waiting to send its first request.
			httpClient.CloseIdleConnections()
			logs, err := filepath.Glob(filepath.Join(temporary, "commander-playground-*", "hosts.log"))
			if err != nil || len(logs) != 1 {
				t.Fatalf("expected one local host log, found %d (%v)", len(logs), err)
			}
			log, err := os.ReadFile(logs[0])
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(log, []byte(password)) || bytes.Contains(log, []byte(token)) {
				t.Error("credentials leaked into host logs")
			}
			if _, err := io.WriteString(keyboard, "/exit\r"); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("/exit did not stop the CLI")
			}
			files, err := os.ReadDir(temporary)
			if err != nil || len(files) != 0 {
				t.Fatalf("temporary state remains after exit: %v (%v)", files, err)
			}
		})
	}
}

func TestCredentialCommand(t *testing.T) {
	t.Setenv("COMMANDER_MCP_URL", "invalid-url")
	t.Setenv("COMMANDER_DEBUG", "invalid-boolean")
	for _, tc := range []struct {
		name, target, username, password, token, want, missing string
	}{
		{name: "API only", target: "api", username: "commander", password: "fixture-password", token: "invalid token", want: "Username: commander\nPassword: fixture-password\n"},
		{name: "MCP only", target: "mcp", username: "invalid:username", password: "fixture-password", token: "fixture-token", want: "Token: fixture-token\n"},
		{name: "missing API username", target: "api", password: "fixture-password", missing: "API_USERNAME and API_PASSWORD must be set"},
		{name: "missing API password", target: "api", username: "commander", missing: "API_USERNAME and API_PASSWORD must be set"},
		{name: "missing MCP token", target: "mcp", missing: "MCP_TOKEN must be set"},
		{name: "invalid API username", target: "api", username: "invalid:username", password: "fixture-password"},
		{name: "invalid API password", target: "api", username: "commander", password: "fixture\npassword"},
		{name: "invalid MCP token", target: "mcp", token: "fixture token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("API_USERNAME", tc.username)
			t.Setenv("API_PASSWORD", tc.password)
			t.Setenv("MCP_TOKEN", tc.token)
			var stdout, stderr bytes.Buffer
			err := cli.Run(t.Context(), []string{"credentials", tc.target}, nil, &stdout, &stderr)
			if stderr.Len() != 0 {
				t.Fatal("credential command wrote startup output or diagnostics")
			}
			if tc.want != "" {
				if err != nil || stdout.String() != tc.want {
					t.Fatal("credential command did not return only the selected environment credentials")
				}
				return
			}
			if err == nil || stdout.Len() != 0 {
				t.Fatal("missing or invalid credentials must fail without printing credentials")
			}
			if tc.missing != "" && (!strings.HasPrefix(err.Error(), tc.missing) || !strings.Contains(err.Error(), "/credentials")) {
				t.Fatal("missing credentials error must identify the environment variables and in-chat lookup")
			}
			for _, secret := range []string{tc.password, tc.token} {
				if secret != "" && strings.Contains(err.Error(), secret) {
					t.Fatal("credential validation error echoed a secret")
				}
			}
		})
	}
}

func TestCredentialCommandArguments(t *testing.T) {
	for _, arguments := range [][]string{{"credentials"}, {"credentials", "unexpected-secret"}, {"credentials", "api", "unexpected-secret"}} {
		var output bytes.Buffer
		err := cli.Run(t.Context(), arguments, nil, &output, &output)
		if err == nil || err.Error() != "usage: commander credentials api|mcp" || output.Len() != 0 {
			t.Fatal("invalid credentials arguments must fail without starting Playground or echoing values")
		}
	}
}

func testAllInOneMCPFailure(t *testing.T) {
	for _, token := range []string{"   ", "invalid\nheader"} {
		t.Run("invalid token", func(t *testing.T) {
			temporary := allInOneEnvironment(t)
			t.Setenv("MCP_TOKEN", token)
			var output bytes.Buffer
			err := cli.Run(t.Context(), []string{"--all-in-one", "--no-color"}, strings.NewReader(""), &output, &output)
			if err == nil || !strings.Contains(err.Error(), "MCP_TOKEN") || strings.Contains(err.Error(), token) {
				t.Fatal("invalid MCP credentials were accepted or disclosed")
			}
			files, err := os.ReadDir(temporary)
			if err != nil || len(files) != 0 {
				t.Fatal("local hosts created state with invalid MCP credentials")
			}
		})
	}
}

func testAllInOneAPIFailure(t *testing.T) {
	for _, name := range []string{"invalid username", "blank password", "occupied port"} {
		t.Run(name, func(t *testing.T) {
			temporary := allInOneEnvironment(t)
			want := "API_USERNAME and API_PASSWORD"
			switch name {
			case "invalid username":
				t.Setenv("API_USERNAME", "invalid:username")
				want = "username must not contain ':'"
			case "blank password":
				t.Setenv("API_PASSWORD", "   ")
			case "occupied port":
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				defer listener.Close()
				t.Setenv("API_LISTEN_ADDRESS", listener.Addr().String())
				want = "listen"
			}
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			var output bytes.Buffer
			err := cli.Run(ctx, []string{"--all-in-one", "--no-color"}, strings.NewReader(""), &output, &output)
			if err == nil || !strings.Contains(err.Error(), want) || ctx.Err() != nil {
				t.Fatalf("API startup failure was not propagated promptly: %v", err)
			}
			if strings.Contains(output.String(), "all-in-one-api-secret") || strings.Contains(err.Error(), "all-in-one-api-secret") {
				t.Error("API credentials leaked into startup diagnostics")
			}
			files, err := os.ReadDir(temporary)
			if err != nil || len(files) != 0 {
				t.Fatalf("temporary state remains after API startup failed: %v (%v)", files, err)
			}
		})
	}
}

func toolDelta(name string, args map[string]any) map[string]any {
	encoded, _ := json.Marshal(args)
	return map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": name, "type": "function", "function": map[string]any{"name": name, "arguments": string(encoded)}}}}
}

type terminalOutput struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *terminalOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}

func (b *terminalOutput) String() string {
	b.mu.Lock()
	output := b.buffer.String()
	b.mu.Unlock()
	return ansi.Strip(output)
}
