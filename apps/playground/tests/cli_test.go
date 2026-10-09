package playground_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/apps/playground/cli"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/domain/reports"
	"github.com/defos-ai/commander/framework/id"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestAllInOne(t *testing.T) {
	t.Run("shared API and MCP workflow", testAllInOneWorkflow)
	t.Run("generated API and MCP credentials", testAllInOneGeneratedCredentials)
	t.Run("API startup failures clean up local hosts", testAllInOneAPIFailure)
	t.Run("invalid MCP token starts no local hosts", testAllInOneMCPFailure)
}

func testAllInOneWorkflow(t *testing.T) {
	// Browser presence satisfies mission preflight; this smoke test only executes SEO.
	// Real browser execution is covered by the MCP suite against Compose.
	temporary := allInOneEnvironment(t)
	browser := filepath.Join(t.TempDir(), "browser-fixture")
	if runtime.GOOS == "windows" {
		browser += ".exe"
	}
	if err := os.WriteFile(browser, []byte("fixture executable, never launched"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CHROMIUM_PATH", browser)
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
			2: {"review-website"},
			3: {"starting_actions", "automations", "input_schema", "output_schema"},
			4: {"web-quality-agent-"},
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
			delta = toolDelta("describe_mission", map[string]any{"mission_name": "review-website"})
		case 4:
			delta = toolDelta("start_operation", map[string]any{"operation_id": operationID, "mission_name": "review-website", "input": map[string]any{"url": website.URL}})
		case 5:
			delta = toolDelta("assign_task", map[string]any{"operation_id": operationID, "action_name": "check-seo", "input": map[string]any{"url": website.URL}})
		case 6:
			delta, finish = map[string]any{"content": "**Review running**"}, "stop"
		case 7, 9:
			// Continuations must reach the model automatically, carrying actual
			// results and then the operator's answer, without another chat prompt.
			last := body.Messages[len(body.Messages)-1]
			_, state, ok := strings.Cut(last.Content, "Current state:\n")
			var op operations.Operation
			if !ok || last.Role != "user" || json.Unmarshal([]byte(state), &op) != nil || op.ID != operationID || len(op.Tasks) != 1 || op.Tasks[0].Result == nil || !bytes.Contains(op.Tasks[0].Result.Output, []byte("Release preview")) {
				t.Errorf("model did not receive saved task evidence: %s", last.Content)
				http.Error(w, "missing operation evidence", http.StatusBadRequest)
				return
			}
			if !strings.HasPrefix(op.Tasks[0].AgentID, "web-quality-agent-") {
				t.Errorf("controller did not record the selected agent: %+v", op.Tasks[0])
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
					"summary": "Release preview reviewed; operator confirmed intentional noindex", "findings": []any{}}})
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
	input, keyboard := io.Pipe()
	defer input.Close()
	defer keyboard.Close()
	var output terminalOutput
	done := make(chan error, 1)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		done <- cli.Run(ctx, []string{"--all-in-one", "--provider=other", "--model=fixture", "--base-url=" + model.URL + "/v1", "--no-color", "--debug=false"}, input, &output, &output)
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
		cancel()
		_ = keyboard.Close()
		select {
		case <-stopped:
		case <-time.After(10 * time.Second):
			t.Error("Playground did not clean up its local hosts")
		}
	})
	wait("Commander connected")
	endpoint := regexp.MustCompile(`http://127\.0\.0\.1:\d+/mcp`).FindString(output.String())
	if endpoint == "" {
		t.Fatalf("missing local endpoint: %s", output.String())
	}
	apiNotice := regexp.MustCompile(`API listening: (http://127\.0\.0\.1:\d+)`).FindStringSubmatch(output.String())
	if len(apiNotice) != 2 {
		t.Fatalf("missing local API endpoint: %s", output.String())
	}
	apiURL := apiNotice[1]
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
	get("/api/v1/operations", "", http.StatusUnauthorized)
	get("/api/v1/operations", "wrong-password", http.StatusUnauthorized)
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

	if _, err := io.WriteString(keyboard, "Review SEO for "+website.URL+"\r"); err != nil {
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
	if apiOperation.ID != operationID || apiOperation.Status != "completed" || apiOperation.ReportID != report.ID || len(apiOperation.Tasks) != 1 {
		t.Fatalf("API did not read the MCP operation: %+v", apiOperation)
	}
	task := apiOperation.Tasks[0]
	if task.ActionName != "check-seo" || task.Status != "succeeded" || task.Result == nil || !bytes.Contains(task.Result.Output, []byte("Release preview")) {
		t.Fatalf("API did not read saved agent evidence: %+v", task)
	}
	var apiReport models.ReportResponse
	if err := json.Unmarshal(get("/api/v1/reports/"+report.ID, "all-in-one-api-secret", http.StatusOK), &apiReport); err != nil {
		t.Fatal(err)
	}
	if apiReport.ID != report.ID || apiReport.OperationID != operationID || !bytes.Contains(apiReport.Output, []byte("operator confirmed intentional noindex")) {
		t.Fatalf("API did not read the MCP report: %+v", apiReport)
	}
	client.Close()
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
	for _, endpoint := range []string{endpoint, apiURL} {
		address, _ := url.Parse(endpoint)
		connection, err := net.DialTimeout("tcp", address.Host, time.Second)
		if err == nil {
			_ = connection.Close()
			t.Errorf("local host %s is still listening after /exit", endpoint)
		}
	}
}

func allInOneEnvironment(t *testing.T) string {
	t.Helper()
	temporary := t.TempDir()
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, temporary)
	}
	t.Setenv("ENV", "")
	t.Setenv("COMMANDER_LLM_API_KEY", "fixture-only")
	t.Setenv("COMMANDER_MCP_URL", "http://127.0.0.1:1/mcp") // The local host wins.
	t.Setenv("COMMANDER_MCP_TOKEN", "invalid remote token") // Local credentials win too.
	t.Setenv("MCP_TOKEN", "all-in-one-mcp-secret")
	t.Setenv("API_USERNAME", "commander")
	t.Setenv("API_PASSWORD", "all-in-one-api-secret")
	t.Setenv("API_LISTEN_ADDRESS", "127.0.0.1:0")
	t.Setenv("PORT", "not-a-port") // All-in-one uses an explicit API listener.
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
			if _, err := io.WriteString(keyboard, "/credentials api\r"); err != nil {
				t.Fatal(err)
			}
			wait("Password:")
			if strings.Contains(output.String(), "Token:") {
				t.Fatal("API credential lookup also displayed the MCP token")
			}
			if _, err := io.WriteString(keyboard, "/credentials mcp\r"); err != nil {
				t.Fatal(err)
			}
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
			httpClient := &http.Client{Timeout: 3 * time.Second}
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
				response.Body.Close()
				if response.StatusCode != attempt.status {
					t.Errorf("API credential authentication status=%d, want %d", response.StatusCode, attempt.status)
				}
			}
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
