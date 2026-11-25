package missiontest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/host"
	missiontest "github.com/defos-ai/commander/framework/testing/missiontest"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/missiontest"
)

type Stack struct {
	API       *fakes.API
	Config    missiontest.Config
	Target    *fakes.Target
	Transport *fakes.DeadlineTransport
}

func NewStack(t *testing.T, mode string) *Stack {
	t.Helper()
	s := &Stack{}
	redirect := ""
	if mode == "redirect" {
		s.Target = new(fakes.Target)
		target := httptest.NewServer(s.Target)
		t.Cleanup(target.Close)
		redirect = target.URL
	}
	s.API = fakes.NewAPI(mode, redirect)
	server := httptest.NewServer(s.API)
	t.Cleanup(server.Close)
	s.Config = config(server.URL)
	base := http.DefaultTransport.(*http.Transport).Clone()
	s.Transport = fakes.NewDeadlineTransport(base)
	s.Config.HTTPClient = &http.Client{Timeout: 15 * time.Second, Transport: s.Transport}
	t.Cleanup(s.Config.HTTPClient.CloseIdleConnections)
	return s
}

func config(url string) missiontest.Config {
	return missiontest.Config{URL: url, Credentials: host.BasicCredentials{Username: fakes.Username, Password: fakes.Password},
		Timeout: time.Second, PollInterval: time.Millisecond}
}

func MissionFile(t *testing.T) string {
	t.Helper()
	mission := missions.Mission{Name: "missiontest-inspection", Objectives: []string{"Inspect supplied data and report its findings."},
		RequiredActions: []string{"inspect", "report"}, StartingActions: []string{"inspect"},
		InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`)}
	raw, err := json.Marshal([]missions.Mission{mission})
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(t.TempDir(), "missions.json")
	if err := os.WriteFile(name, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return name
}

func FailureProcess(t *testing.T, mode string, settings missiontest.Config) ([]byte, error) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, "-test.run=^TestMissiontestFailureProcess$", "-test.count=1")
	command.Env = append(os.Environ(), "COMMANDER_MISSIONTEST_FAILURE="+mode, "COMMANDER_MISSIONTEST_URL="+settings.URL)
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("missiontest failure subprocess did not stop: %v", ctx.Err())
	}
	return output, err
}

func FailureMode() string { return os.Getenv("COMMANDER_MISSIONTEST_FAILURE") }

func FailureConfig(t *testing.T) missiontest.Config {
	t.Helper()
	url := os.Getenv("COMMANDER_MISSIONTEST_URL")
	if url == "" {
		t.Fatal("missiontest failure subprocess has no owned test API")
	}
	return config(url)
}
