package actions

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/defos-ai/commander/apps/agents/builder/internal/github"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/agent/manifest"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/id"
)

const ReleaseAgentName = "release-agent"
const ReleaseAgentDescription = "Release an operator-approved source commit through the generated repository's make-release.yml workflow. Supply the saved publish-agent commit, build-agent agent_type, GitHub owner/repository and an explicitly chosen stable version. Uses managed GITHUB_TOKEN with Actions write and release read access. A pending result proposes another status check with workflow_run_id; preserve it to observe the same run without dispatching again. Failed runs must be inspected and retried in GitHub in the same run. An uncertain dispatch must be inspected before submitting a new release task. Returns an exact package reference only after the entire workflow succeeds and its published manifest, native asset checksums and version tag are verified. Does not install the agent or change repository/package visibility."

type ReleaseAgentInput struct {
	Repository    string `json:"repository"`
	CommitSHA     string `json:"commit_sha"`
	Version       string `json:"version"`
	AgentType     string `json:"agent_type"`
	WorkflowRunID int64  `json:"workflow_run_id,omitempty"`
}

type ReleaseAgentOutput struct {
	Repository    string `json:"repository"`
	CommitSHA     string `json:"commit_sha"`
	Version       string `json:"version"`
	AgentType     string `json:"agent_type"`
	Visibility    string `json:"visibility"`
	Status        string `json:"status"`
	WorkflowRunID int64  `json:"workflow_run_id,omitempty"`
	WorkflowURL   string `json:"workflow_url,omitempty"`
	Conclusion    string `json:"conclusion,omitempty"`
	Package       string `json:"package,omitempty"`
	ReleaseURL    string `json:"release_url,omitempty"`
}

type ReleaseAgent struct{ *action.Base }

func NewReleaseAgent() (*ReleaseAgent, error) {
	input, err := action.SchemaFor[ReleaseAgentInput]()
	if err != nil {
		return nil, err
	}
	output, err := action.SchemaFor[ReleaseAgentOutput]()
	if err != nil {
		return nil, err
	}
	return &ReleaseAgent{Base: action.NewBase(actionmodel.Action{
		Name:            ReleaseAgentName,
		Description:     ReleaseAgentDescription,
		RequiredSecrets: []string{"GITHUB_TOKEN"}, InputSchema: input, OutputSchema: output,
	})}, nil
}

type releaseIntent struct {
	Input        ReleaseAgentInput `json:"input"`
	RepositoryID int64             `json:"repository_id"`
	WorkflowID   int64             `json:"workflow_id"`
	Branch       string            `json:"branch"`
}

func (a *ReleaseAgent) Run(ctx context.Context, raw json.RawMessage) (action.Result, error) {
	if err := ctx.Err(); err != nil {
		return action.Result{}, err
	}
	if err := a.Check(); err != nil {
		return action.Result{}, err
	}
	var input ReleaseAgentInput
	if err := action.DecodeInput(raw, &input); err != nil {
		return action.Result{}, err
	}
	if err := validateReleaseInput(input); err != nil {
		return action.Result{}, err
	}
	execution, ok := action.ExecutionFromContext(ctx)
	if !ok || !id.Valid(execution.CommandID) {
		return action.Result{}, errors.New("release-agent requires a controller-dispatched execution")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	saved, found, err := loadReleaseIntent(ctx, input)
	if err != nil {
		return action.Result{}, err
	}
	token, err := action.Secret(ctx, "GITHUB_TOKEN")
	if err != nil {
		return action.Result{}, err
	}
	defer clear(token)
	if len(token) == 0 || strings.ContainsAny(string(token), "\x00\r\n") {
		return action.Result{}, action.ErrSecretUnavailable
	}
	var result action.Result
	err = action.RunWithProgress(ctx, "Release request handling", func() error {
		var err error
		result, err = a.release(ctx, input, saved, found, token, execution.CommandID)
		return err
	})
	return result, err
}

func (a *ReleaseAgent) release(ctx context.Context, input ReleaseAgentInput, saved releaseIntent, found bool, token []byte, commandID string) (action.Result, error) {
	client := github.Client{Token: token}
	repository, err := client.GetRepository(ctx, input.Repository)
	if err != nil {
		return action.Result{}, err
	}
	workflow, err := client.GetWorkflow(ctx, input.Repository)
	if err != nil {
		return action.Result{}, err
	}
	if workflow.ID <= 0 || workflow.Path != ".github/workflows/make-release.yml" || workflow.State != "active" || repository.DefaultBranch == "" {
		return action.Result{}, errors.New("the repository needs an active make-release.yml workflow on its default branch")
	}
	intent := releaseIntent{Input: input, RepositoryID: repository.ID, WorkflowID: workflow.ID, Branch: repository.DefaultBranch}
	if found {
		if !reflect.DeepEqual(saved, intent) {
			return action.Result{}, errors.New("release repository, workflow or default branch changed; inspect before retrying")
		}
	} else {
		if input.WorkflowRunID == 0 {
			sha, err := client.GetCommit(ctx, input.Repository, repository.DefaultBranch)
			if err != nil {
				return action.Result{}, err
			}
			if sha != input.CommitSHA {
				return action.Result{}, errors.New("the default branch differs from the approved source commit; review it before releasing")
			}
			if err := client.VerifyWorkflow(ctx, input.Repository, input.CommitSHA); err != nil {
				return action.Result{}, err
			}
		}
		if err := saveReleaseCheckpoint(ctx, "release-intent", intent); err != nil {
			return action.Result{}, err
		}
	}
	output := ReleaseAgentOutput{Repository: input.Repository, CommitSHA: input.CommitSHA, Version: input.Version, AgentType: input.AgentType, Visibility: "public"}
	if repository.Private {
		output.Visibility = "private"
	}
	runID := input.WorkflowRunID
	if runID == 0 {
		runID, err = dispatchRelease(ctx, client, intent, commandID)
		if err != nil {
			return action.Result{}, err
		}
		if runID == 0 {
			output.Status = "uncertain"
			return action.JSONResult(output)
		}
	}
	return observeRelease(ctx, client, intent, runID, output, commandID)
}

func validateReleaseInput(input ReleaseAgentInput) error {
	if err := github.ValidateRepository(input.Repository); err != nil {
		return err
	}
	sha, err := hex.DecodeString(input.CommitSHA)
	if err != nil || len(sha) != 20 || input.CommitSHA != strings.ToLower(input.CommitSHA) {
		return errors.New("commit_sha must be the approved source commit's 40 lowercase hex characters")
	}
	if !manifest.ValidVersion(input.Version) {
		return errors.New("version must be a stable vMAJOR.MINOR.PATCH")
	}
	if _, ok := id.RuntimeType(input.AgentType+"-12345678", id.Agent); !ok || input.AgentType == "example-agent" {
		return errors.New("agent_type must be the generated agent's distinct type from build-agent")
	}
	if input.WorkflowRunID < 0 {
		return errors.New("workflow_run_id must be positive when supplied")
	}
	return nil
}

func loadReleaseIntent(ctx context.Context, input ReleaseAgentInput) (releaseIntent, bool, error) {
	data, found, err := action.LoadCheckpoint(ctx, "release-intent")
	if err != nil || !found {
		return releaseIntent{}, false, err
	}
	var saved releaseIntent
	if json.Unmarshal(data, &saved) != nil || validateReleaseInput(saved.Input) != nil || saved.RepositoryID <= 0 || saved.WorkflowID <= 0 || saved.Branch == "" {
		return releaseIntent{}, false, errors.New("invalid release intent checkpoint")
	}
	if saved.Input != input {
		return releaseIntent{}, false, errors.New("release input conflicts with its saved repository, commit, type, version or run")
	}
	return saved, true, nil
}

func saveReleaseCheckpoint(ctx context.Context, name string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return errors.New("cannot encode release checkpoint")
	}
	return action.SaveCheckpoint(ctx, name, data)
}

func dispatchRelease(ctx context.Context, client github.Client, intent releaseIntent, requestID string) (int64, error) {
	data, found, err := action.LoadCheckpoint(ctx, "release-run")
	if err != nil {
		return 0, err
	}
	var saved struct {
		ID int64 `json:"id"`
	}
	if found {
		if json.Unmarshal(data, &saved) != nil || saved.ID <= 0 {
			return 0, errors.New("invalid release run checkpoint")
		}
		return saved.ID, nil
	}
	data, attempted, err := action.LoadCheckpoint(ctx, "release-dispatch-attempt")
	if err != nil {
		return 0, err
	}
	if attempted && string(data) != `{"requested":true}` {
		return 0, errors.New("invalid release dispatch checkpoint")
	}
	if !attempted {
		// Persist before sending: an uncertain acknowledgement never permits
		// another dispatch for this execution.
		if err := action.SaveCheckpoint(ctx, "release-dispatch-attempt", json.RawMessage(`{"requested":true}`)); err != nil {
			return 0, err
		}
		saved.ID, err = client.DispatchRelease(ctx, intent.Input.Repository, intent.Branch, intent.Input.Version, intent.Input.CommitSHA, requestID)
		if ctx.Err() != nil {
			return 0, fmt.Errorf("release dispatch may have succeeded; inspect GitHub before a new task: %w", ctx.Err())
		}
	}
	if saved.ID == 0 {
		// GitHub can accept a request before the caller receives its run ID.
		// Reconcile by the execution UUID carried in the workflow run name.
		saved.ID, err = client.FindReleaseRun(ctx, intent.Input.Repository, intent.Input.CommitSHA, intent.Input.Version, requestID)
		if err != nil {
			return 0, fmt.Errorf("release dispatch is uncertain; inspect GitHub before a new task: %w", err)
		}
	}
	if saved.ID == 0 {
		return 0, nil
	}
	if err := saveReleaseCheckpoint(ctx, "release-run", saved); err != nil {
		return 0, err
	}
	return saved.ID, nil
}

func observeRelease(ctx context.Context, client github.Client, intent releaseIntent, runID int64, output ReleaseAgentOutput, commandID string) (action.Result, error) {
	output.WorkflowRunID = runID
	output.WorkflowURL = "https://github.com/" + intent.Input.Repository + "/actions/runs/" + strconv.FormatInt(runID, 10)
	deadline := time.Now().Add(time.Minute)
	poll := intent.Input.WorkflowRunID != 0
	for {
		run, err := client.GetWorkflowRun(ctx, intent.Input.Repository, runID)
		if err != nil {
			return action.Result{}, err
		}
		if run.ID != runID || run.WorkflowID != intent.WorkflowID || run.HeadSHA != intent.Input.CommitSHA || run.Event != "workflow_dispatch" || run.RepositoryID != intent.RepositoryID || run.HeadBranch != intent.Branch {
			return action.Result{}, errors.New("release workflow run does not match the approved repository, workflow and source commit")
		}
		requestID, matches := strings.CutPrefix(run.DisplayTitle, "Release agent "+intent.Input.Version+" ")
		if !matches || !id.Valid(requestID) || !poll && requestID != commandID {
			return action.Result{}, errors.New("release workflow run does not identify the requested version and release request")
		}
		if run.Status == "completed" {
			if run.Conclusion != "success" {
				output.Status = "failed"
				switch run.Conclusion {
				case "failure", "cancelled", "timed_out", "action_required", "neutral", "skipped", "stale", "startup_failure":
					output.Conclusion = run.Conclusion
				}
				return action.JSONResult(output)
			}
			if err := client.VerifyRelease(ctx, intent.Input.Repository, intent.Input.Version, intent.Input.CommitSHA, intent.Input.AgentType); err != nil {
				return action.Result{}, err
			}
			output.Status = "released"
			output.Package = "github.com/" + intent.Input.Repository + "@" + intent.Input.Version
			output.ReleaseURL = "https://github.com/" + intent.Input.Repository + "/releases/tag/" + intent.Input.Version
			return action.JSONResult(output)
		}
		switch run.Status {
		case "queued", "in_progress", "waiting", "pending", "requested":
		default:
			return action.Result{}, errors.New("GitHub returned an unknown release workflow status")
		}
		if !poll || !time.Now().Before(deadline) {
			output.Status = "pending"
			result, err := action.JSONResult(output)
			if err != nil {
				return action.Result{}, err
			}
			next := intent.Input
			next.WorkflowRunID = runID
			input, _ := json.Marshal(next)
			result.NextActions = []action.NextAction{{ActionName: ReleaseAgentName, Input: input, Reason: "Wait for this release workflow and verify its published package; observe the same run without dispatching another."}}
			return result, nil
		}
		timer := time.NewTimer(10 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return action.Result{}, ctx.Err()
		case <-timer.C:
		}
	}
}
