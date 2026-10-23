package playground_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/playground/cli"
	"github.com/defos-ai/commander/apps/playground/internal/local"
)

func testAllInOneAdmin(t *testing.T) {
	t.Run("serves UI and stops with session", func(t *testing.T) {
		temporary := allInOneEnvironment(t)
		session, stop := startLocalAdminSession(t, local.Options{})
		body, _ := fetchLocalAsset(t, session.AdminURL, "/")
		if string(body) != "<html>Admin fixture</html>" {
			t.Fatal("all-in-one Admin did not serve the explicit frontend directory")
		}
		deepLink, _ := fetchLocalAsset(t, session.AdminURL, "/connections/local/operations/example")
		if !bytes.Equal(body, deepLink) {
			t.Fatal("Admin deep link did not serve its frontend")
		}
		health, _ := fetchLocalAsset(t, session.AdminURL, "/healthz")
		if string(health) != "ok\n" {
			t.Fatal("Admin health check failed")
		}
		connections, _ := fetchLocalAsset(t, session.AdminURL, "/ui/connections")
		var result struct {
			Items []json.RawMessage `json:"items"`
		}
		if json.Unmarshal(connections, &result) != nil || result.Items == nil || len(result.Items) != 0 {
			t.Fatal("Admin connection routes did not start without saved credentials")
		}
		for _, secret := range []string{session.APICredentials.Password, session.MCPCredentials.Token} {
			if bytes.Contains(body, []byte(secret)) || bytes.Contains(connections, []byte(secret)) {
				t.Fatal("Admin response exposed session credentials")
			}
		}
		checkLocalAdminProxy(t, session)
		stop()
		assertLocalHostStopped(t, session.AdminURL)
		files, err := os.ReadDir(temporary)
		if err != nil || len(files) != 0 {
			t.Fatalf("temporary state remains after Admin stopped: %v (%v)", files, err)
		}
	})
	t.Run("wildcard listeners expose usable local URLs", func(t *testing.T) {
		allInOneEnvironment(t)
		t.Setenv("API_LISTEN_ADDRESS", "0.0.0.0:0")
		t.Setenv("UI_LISTEN_ADDRESS", "0.0.0.0:0")
		session, _ := startLocalAdminSession(t, local.Options{})
		for _, endpoint := range []string{session.APIURL, session.AdminURL} {
			address, err := url.Parse(endpoint)
			if err != nil || !net.ParseIP(address.Hostname()).IsLoopback() {
				t.Fatal("wildcard listener was not advertised with a usable loopback URL")
			}
		}
		checkLocalAdminProxy(t, session)
	})
	for _, name := range []string{"disabled", "occupied port", "missing frontend"} {
		t.Run(name, func(t *testing.T) {
			allInOneEnvironment(t)
			options := local.Options{}
			switch name {
			case "disabled":
				options.DisableAdmin = true
				// Invalid settings must be ignored when the optional host is disabled.
				t.Setenv("UI_LISTEN_ADDRESS", "invalid-address")
				t.Setenv("UI_STATIC_DIR", filepath.Join(t.TempDir(), "missing"))
			case "occupied port":
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				defer listener.Close()
				t.Setenv("UI_LISTEN_ADDRESS", listener.Addr().String())
			case "missing frontend":
				t.Setenv("UI_STATIC_DIR", filepath.Join(t.TempDir(), "missing"))
			}
			session, stop := startLocalAdminSession(t, options)
			if session.AdminURL != "" || session.Context.Err() != nil {
				t.Fatal("unavailable Admin advertised a URL or stopped the session")
			}
			fetchLocalAsset(t, session.APIURL, "/readyz")
			notices := strings.Join(session.Notices, "\n")
			if strings.Contains(notices, "Admin") != (name != "disabled") {
				t.Fatal("Admin failure notice did not match whether startup was requested")
			}
			for _, secret := range []string{session.APICredentials.Password, session.MCPCredentials.Token} {
				if strings.Contains(notices, secret) {
					t.Fatal("Admin startup notice exposed credentials")
				}
			}
			stop() // An optional startup failure must not turn session shutdown into an error.
		})
	}
}

func checkLocalAdminProxy(t *testing.T, session *local.Session) {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 3 * time.Second, Jar: jar}
	defer client.CloseIdleConnections()
	request := func(method, path string, input []byte, status int) []byte {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), method, session.AdminURL+path, bytes.NewReader(input))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-Commander-UI", "1")
		req.Header.Set("Origin", session.AdminURL)
		req.Header.Set("Content-Type", "application/json")
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil || response.StatusCode != status {
			t.Fatalf("Admin %s %s: status=%d, want %d, read error=%v", method, path, response.StatusCode, status, err)
		}
		if bytes.Contains(body, []byte(session.APICredentials.Password)) || bytes.Contains(body, []byte(session.MCPCredentials.Token)) {
			t.Fatal("Admin authentication or proxy response exposed credentials")
		}
		return body
	}
	proxyPath := "/ui/proxy?" + url.Values{"url": {session.APIURL}, "path": {"/api/v1/agents"}}.Encode()
	for _, attempt := range []struct {
		password string
		status   int
	}{
		{"wrong-password", http.StatusUnauthorized},
		{session.APICredentials.Password, http.StatusOK},
	} {
		input, err := json.Marshal(map[string]string{"url": session.APIURL, "username": session.APICredentials.Username, "password": attempt.password})
		if err != nil {
			t.Fatal(err)
		}
		request(http.MethodPost, "/ui/connections", input, attempt.status)
		if attempt.status == http.StatusUnauthorized {
			request(http.MethodGet, proxyPath, nil, http.StatusUnauthorized)
		}
	}
	body := request(http.MethodGet, proxyPath, nil, http.StatusOK)
	var agents struct {
		Items []struct{ Type string } `json:"items"`
	}
	if json.Unmarshal(body, &agents) != nil || len(agents.Items) != 2 {
		t.Fatal("Admin proxy did not return the embedded API's registered agents")
	}
	var types []string
	for _, agent := range agents.Items {
		types = append(types, agent.Type)
	}
	slices.Sort(types)
	if !slices.Equal(types, []string{"builder-agent", "spawner-agent"}) {
		t.Fatal("Admin proxy did not return the embedded builder and spawner")
	}
}

func TestNoAdminRequiresAllInOne(t *testing.T) {
	var output bytes.Buffer
	err := cli.Run(t.Context(), []string{"--no-admin"}, strings.NewReader(""), &output, &output)
	if err == nil || !strings.Contains(err.Error(), "--all-in-one") {
		t.Fatal("--no-admin was accepted for a remote Playground session")
	}
}

func startLocalAdminSession(t *testing.T, options local.Options) (*local.Session, func()) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	t.Cleanup(cancel)
	session, err := local.StartWithOptions(ctx, options)
	if err != nil {
		t.Fatalf("start all-in-one: %v", err)
	}
	stop := func() {
		if err := session.Close(); err != nil {
			t.Errorf("stop all-in-one: %v", err)
		}
	}
	t.Cleanup(stop)
	return session, stop
}

func fetchLocalAsset(t *testing.T, baseURL, path string) ([]byte, http.Header) {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, baseURL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Commander-UI", "1")
	client := &http.Client{Timeout: 3 * time.Second}
	defer client.CloseIdleConnections()
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusOK || len(body) == 0 {
		t.Fatalf("fetch %s: status=%d error=%v", path, response.StatusCode, err)
	}
	return body, response.Header
}

func assertLocalHostStopped(t *testing.T, endpoint string) {
	t.Helper()
	address, err := url.Parse(endpoint)
	if err != nil || address.Host == "" {
		t.Fatal("local host did not expose a valid endpoint")
	}
	connection, err := net.DialTimeout("tcp", address.Host, time.Second)
	if err == nil {
		connection.Close()
		t.Fatal("Admin is still listening after session close")
	}
}
