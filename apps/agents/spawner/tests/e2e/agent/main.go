// Command agent is the package served by the isolated API/MCP test stacks.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/comms"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/artifacts"
	"github.com/defos-ai/commander/framework/id"
	fsprovider "github.com/defos-ai/commander/providers/fs"
	fsartifacts "github.com/defos-ai/commander/providers/fs/artifacts"
	fscomms "github.com/defos-ai/commander/providers/fs/comms"
	rabbitmqcomms "github.com/defos-ai/commander/providers/rabbitmq/comms"
	s3artifacts "github.com/defos-ai/commander/providers/s3/artifacts"
	s3store "github.com/defos-ai/commander/providers/s3/store"
)

const agentType = "fixture-agent"

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx); err != nil {
		// Provider errors can contain connection details; never echo the bootstrap
		// environment or a raw connection error into container logs.
		fmt.Fprintln(os.Stderr, "fixture-agent stopped with an error")
		os.Exit(1)
	}
}

func run(ctx context.Context) (result error) {
	agentID, err := id.AgentIDFromSuffix(agentType, os.Getenv("COMMANDER_AGENT_ID_SUFFIX"))
	if err != nil {
		return err
	}
	store, err := openStorage(ctx, os.Getenv("STORAGE_URL"))
	if err != nil {
		return err
	}
	bus, err := openBus(ctx, os.Getenv("BUS_URL"))
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, bus.Close()) }()
	echo, err := action.NewFunc(action.FuncConfig[echoInput, echoOutput]{
		Action: actionmodel.Action{Name: "fixture-echo", Description: "Echo the supplied message and publish it as a text artifact."},
		Run: func(_ context.Context, input echoInput) (echoOutput, error) {
			return echoOutput{Message: input.Message, AgentID: agentID}, nil
		},
	})
	if err != nil {
		return err
	}
	interval := time.Second
	pauseContext, stopPauses := context.WithCancel(ctx)
	executor := &echoAction{Func: echo, pauseContext: pauseContext,
		client: &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{Proxy: nil},
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	defer func() {
		stopPauses()
		executor.pauses.Wait()
		executor.client.CloseIdleConnections()
	}()
	instance, err := agent.NewAgent(agent.AgentConfig{
		ID: agentID, Type: agentType, Actions: []action.Executor{executor},
		ArtifactFileStore: store, HeartbeatInterval: &interval,
	})
	if err != nil {
		return err
	}
	err = instance.Serve(ctx, bus, nil)
	// Serve joins cleanup errors. Unwrap a lone error so normal signal shutdown
	// succeeds, while a joined transport or persistence failure still fails.
	for {
		joined, ok := err.(interface{ Unwrap() []error })
		if !ok || len(joined.Unwrap()) != 1 {
			break
		}
		err = joined.Unwrap()[0]
	}
	if ctx.Err() != nil && err == context.Cause(ctx) {
		return nil
	}
	return err
}

type echoInput struct {
	Message   string `json:"message"`
	ResumeURL string `json:"resume_url,omitempty"`
}

func (i echoInput) Validate() error {
	if i.ResumeURL != "" {
		const prefix = "http://fixture-releases:8080/gates/"
		if !strings.HasPrefix(i.ResumeURL, prefix) || !id.Valid(strings.TrimPrefix(i.ResumeURL, prefix)) {
			return errors.New("resume_url must identify a fixture release gate")
		}
	}
	return nil
}

type echoOutput struct {
	Message string `json:"message"`
	AgentID string `json:"agent_id"`
}

type echoAction struct {
	*action.Func[echoInput, echoOutput]
	pauseContext context.Context
	pauses       sync.WaitGroup
	client       *http.Client
}

func (a *echoAction) Run(ctx context.Context, input json.RawMessage) (action.Result, error) {
	result, err := a.Func.Run(ctx, input)
	if err != nil {
		return action.Result{}, err
	}
	var output echoOutput
	if err := json.Unmarshal(result.Output, &output); err != nil {
		return action.Result{}, err
	}
	var request echoInput
	if err := json.Unmarshal(input, &request); err != nil {
		return action.Result{}, err
	}
	if request.ResumeURL != "" {
		a.SetUnavailable("Waiting for the test fixture to restore availability")
		// Keep the pause independent of the completed task, but bounded by both
		// a timeout and the host lifetime so failed tests cannot strand the agent.
		a.pauses.Add(1)
		go func() {
			defer a.pauses.Done()
			defer a.SetStatus(action.Ready)
			req, err := http.NewRequestWithContext(a.pauseContext, http.MethodGet, request.ResumeURL, nil)
			if err != nil {
				return
			}
			if response, err := a.client.Do(req); err == nil {
				response.Body.Close()
			}
		}()
	}
	result.Artifacts = []action.Artifact{{
		Name: "Echo evidence",
		Files: []artifacts.FileSource{{
			Name: "echo.txt", MediaType: "text/plain", Data: []byte(output.Message),
		}},
	}}
	return result, nil
}

func openBus(ctx context.Context, raw string) (comms.Bus, error) {
	parsed, err := url.Parse(raw)
	if err != nil || raw == "" {
		return nil, errors.New("BUS_URL must be a valid URL")
	}
	switch parsed.Scheme {
	case "", "file", "fs":
		path, err := fsprovider.FilesystemPath(raw)
		if err != nil {
			return nil, err
		}
		return fscomms.NewFsBus(path)
	case "amqp", "amqps":
		return rabbitmqcomms.NewRabbitMQBus(ctx, raw, rabbitmqcomms.Options{})
	default:
		return nil, errors.New("unsupported BUS_URL scheme")
	}
}

func openStorage(ctx context.Context, raw string) (artifacts.ArtifactFileStore, error) {
	if scheme, _, ok := strings.Cut(raw, ":"); ok && strings.EqualFold(scheme, "s3") {
		store, err := s3store.Open(ctx, raw)
		if err != nil {
			return nil, err
		}
		return s3artifacts.NewArtifactFileS3Store(store)
	}
	parsed, err := url.Parse(raw)
	if err != nil || raw == "" {
		return nil, errors.New("STORAGE_URL must be a valid URL")
	}
	switch parsed.Scheme {
	case "", "file", "fs":
		path, err := fsprovider.FilesystemPath(raw)
		if err != nil {
			return nil, err
		}
		return fsartifacts.NewArtifactFileFsStore(path)
	default:
		return nil, errors.New("unsupported STORAGE_URL scheme")
	}
}
