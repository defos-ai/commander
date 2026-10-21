package actions

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"time"

	"github.com/defos-ai/commander/apps/agents/builder/internal/git"
	"github.com/defos-ai/commander/apps/agents/builder/internal/github"
	"github.com/defos-ai/commander/apps/agents/builder/internal/process"
	"github.com/defos-ai/commander/apps/agents/builder/internal/publish"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/agent/environment"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/artifacts"
	"github.com/defos-ai/commander/framework/id"
)

const PublishAgentName = "publish-agent"

type PublishAgentInput struct {
	Repository       string                 `json:"repository"`
	SourceArtifactID string                 `json:"source_artifact_id"`
	SourceFile       artifacts.ArtifactFile `json:"source_file"`
	Visibility       string                 `json:"visibility,omitempty"`
}

type PublishAgentOutput struct {
	RepositoryURL string `json:"repository_url"`
	CommitURL     string `json:"commit_url"`
	CommitSHA     string `json:"commit_sha"`
	Visibility    string `json:"visibility"`
	Branch        string `json:"branch"`
}

type PublishAgent struct {
	*action.Base
	files             artifacts.ArtifactFileStore
	defaultVisibility string
	executable        atomic.Pointer[string]
}

func NewPublishAgent(files artifacts.ArtifactFileStore, defaultVisibility string) (*PublishAgent, error) {
	if files == nil {
		return nil, errors.New("publish-agent requires artifact file storage")
	}
	if err := ValidateDefaultRepoVisibility(defaultVisibility); err != nil {
		return nil, err
	}
	input, err := action.SchemaFor[PublishAgentInput]()
	if err != nil {
		return nil, err
	}
	var schema map[string]any
	if err := json.Unmarshal(input, &schema); err != nil {
		return nil, err
	}
	properties := schema["properties"].(map[string]any)
	visibility := properties["visibility"].(map[string]any)
	visibility["enum"] = []string{"public", "private"}
	visibility["description"] = "Use the operator's choice or the advertised default. Without either, ask: Share this agent so others can install and improve it? Public (recommended) or Private. Wait for the real answer before publishing."
	if defaultVisibility == "" {
		schema["required"] = append(schema["required"].([]any), "visibility")
	} else {
		visibility["default"] = defaultVisibility
	}
	input, err = json.Marshal(schema)
	if err != nil {
		return nil, err
	}
	output, err := action.SchemaFor[PublishAgentOutput]()
	if err != nil {
		return nil, err
	}
	return &PublishAgent{files: files, defaultVisibility: defaultVisibility, Base: action.NewBase(actionmodel.Action{
		Name:            PublishAgentName,
		Description:     "Publish a reviewed source.tar.gz artifact to a new or empty GitHub repository. Pass the exact saved source artifact ID and file descriptor. Choose public/private explicitly or use the schema default; without either, ask the operator once and recommend Public. Existing repository visibility is verified and never changed; existing unrelated history is never overwritten. Uses managed GITHUB_TOKEN, never token values in input. Returns repository and commit links; source publication does not create an installable agent release.",
		RequiredSecrets: []string{"GITHUB_TOKEN"}, InputSchema: input, OutputSchema: output,
	})}, nil
}

func ValidateDefaultRepoVisibility(value string) error {
	if value != "" && value != "public" && value != "private" {
		return errors.New("BUILDER_DEFAULT_REPO_VISIBILITY must be public, private or unset")
	}
	return nil
}

func (a *PublishAgent) Discover(ctx context.Context, env environment.Environment, scope *environment.DiscoveryScope) (action.Status, error) {
	a.executable.Store(nil)
	a.SetStatus(action.Unknown)
	if env == nil {
		env = &environment.Local{}
	}
	executable, err := env.FindExecutable(ctx, "git", scope)
	if err != nil {
		return action.Unknown, err
	}
	if err := ctx.Err(); err != nil {
		return action.Unknown, err
	}
	if executable == "" {
		a.SetUnavailable("Install Git and run discovery again", actionmodel.Requirement{Kind: actionmodel.ExecutableRequirement, Name: "git"})
		return action.Unavailable, nil
	}
	a.executable.Store(&executable)
	a.SetStatus(action.Ready)
	return action.Ready, nil
}

type publicationIntent struct {
	Input     PublishAgentInput `json:"input"`
	CreatedAt time.Time         `json:"created_at"`
	CommitSHA string            `json:"commit_sha"`
}

func (a *PublishAgent) Run(ctx context.Context, raw json.RawMessage) (action.Result, error) {
	if err := ctx.Err(); err != nil {
		return action.Result{}, err
	}
	if err := a.Check(); err != nil {
		return action.Result{}, err
	}
	var input PublishAgentInput
	if err := action.DecodeInput(raw, &input); err != nil {
		return action.Result{}, err
	}
	if err := validatePublicationInput(input); err != nil {
		return action.Result{}, err
	}
	execution, ok := action.ExecutionFromContext(ctx)
	if !ok || !id.Valid(execution.CommandID) {
		return action.Result{}, errors.New("publish-agent requires a controller-dispatched execution")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	intent, found, err := a.intent(ctx, input)
	if err != nil {
		return action.Result{}, err
	}
	executable := a.executable.Load()
	if executable == nil || !process.Available(*executable) {
		a.executable.Store(nil)
		a.SetStatus(action.Unknown)
		return action.Result{}, errors.New("Git changed; run discovery again")
	}
	token, err := action.Secret(ctx, "GITHUB_TOKEN")
	if err != nil {
		return action.Result{}, err
	}
	defer clear(token)
	if len(token) == 0 || strings.ContainsAny(string(token), "\x00\r\n") {
		return action.Result{}, action.ErrSecretUnavailable
	}
	archive, err := a.files.Get(ctx, intent.Input.SourceArtifactID, intent.Input.SourceFile)
	if err != nil {
		if ctx.Err() != nil {
			return action.Result{}, ctx.Err()
		}
		return action.Result{}, errors.New("cannot read the source artifact; use its exact saved file descriptor")
	}
	directory, err := os.MkdirTemp("", "commander-publish-")
	if err != nil {
		return action.Result{}, errors.New("cannot create publication snapshot")
	}
	defer os.RemoveAll(directory)
	source, home := filepath.Join(directory, "source"), filepath.Join(directory, "home")
	for _, path := range []string{source, home} {
		if err := os.Mkdir(path, 0700); err != nil {
			return action.Result{}, errors.New("cannot prepare publication snapshot")
		}
	}
	if err := publish.ExtractSource(ctx, archive, source, token); err != nil {
		return action.Result{}, err
	}
	command := git.Command{Executable: *executable, Home: home}
	sha, err := command.CommitSnapshot(ctx, source, intent.CreatedAt, execution.CommandID)
	if err != nil {
		return action.Result{}, err
	}
	if found && sha != intent.CommitSHA {
		return action.Result{}, errors.New("publication snapshot differs from its saved commit; inspect before retrying")
	}
	intent.CommitSHA = sha
	if !found {
		data, _ := json.Marshal(intent)
		if err := action.SaveCheckpoint(ctx, "publication-intent", data); err != nil {
			return action.Result{}, err
		}
	}
	return a.publish(ctx, command, source, intent, token)
}

func validatePublicationInput(input PublishAgentInput) error {
	if err := github.ValidateRepository(input.Repository); err != nil {
		return err
	}
	if input.Visibility != "" && input.Visibility != "public" && input.Visibility != "private" {
		return errors.New("visibility must be public or private")
	}
	if !id.Valid(input.SourceArtifactID) || input.SourceFile.Validate() != nil || input.SourceFile.Name != "source.tar.gz" || input.SourceFile.MediaType != "application/gzip" || input.SourceFile.Size <= 0 || input.SourceFile.Size > publish.MaxArchiveBytes {
		return errors.New("publication requires the saved source.tar.gz artifact ID and complete file descriptor")
	}
	return nil
}

func (a *PublishAgent) intent(ctx context.Context, input PublishAgentInput) (publicationIntent, bool, error) {
	data, found, err := action.LoadCheckpoint(ctx, "publication-intent")
	if err != nil {
		return publicationIntent{}, false, err
	}
	if !found {
		if input.Visibility == "" {
			input.Visibility = a.defaultVisibility
		}
		if input.Visibility == "" {
			return publicationIntent{}, false, errors.New("ask the operator to choose Public (recommended) or Private before publishing")
		}
		return publicationIntent{Input: input, CreatedAt: time.Now().UTC().Truncate(time.Second)}, false, nil
	}
	var saved publicationIntent
	if err := json.Unmarshal(data, &saved); err != nil || validatePublicationInput(saved.Input) != nil || saved.Input.Visibility == "" || saved.CreatedAt.IsZero() {
		return publicationIntent{}, false, errors.New("invalid publication checkpoint")
	}
	sha, err := hex.DecodeString(saved.CommitSHA)
	if err != nil || len(sha) != 20 || saved.CommitSHA != strings.ToLower(saved.CommitSHA) {
		return publicationIntent{}, false, errors.New("invalid publication commit checkpoint")
	}
	if input.Visibility == "" {
		input.Visibility = saved.Input.Visibility
	}
	if !reflect.DeepEqual(input, saved.Input) {
		return publicationIntent{}, false, errors.New("publication input conflicts with its saved source, repository or visibility")
	}
	return saved, true, nil
}

func (a *PublishAgent) publish(ctx context.Context, command git.Command, source string, intent publicationIntent, token []byte) (action.Result, error) {
	data, found, err := action.LoadCheckpoint(ctx, "publication-repository")
	if err != nil {
		return action.Result{}, err
	}
	var saved struct {
		ID int64 `json:"id"`
	}
	if found {
		if json.Unmarshal(data, &saved) != nil || saved.ID <= 0 {
			return action.Result{}, errors.New("invalid publication repository checkpoint")
		}
	}
	client := github.Client{Token: token}
	repository, err := client.GetRepository(ctx, intent.Input.Repository)
	if errors.Is(err, github.ErrNotFound) && !found {
		repository, err = client.CreateRepository(ctx, intent.Input.Repository, intent.Input.Visibility)
	}
	if err != nil {
		return action.Result{}, err
	}
	if repository.Private != (intent.Input.Visibility == "private") {
		return action.Result{}, errors.New("existing repository visibility differs; publish-agent never changes it")
	}
	if found {
		if saved.ID != repository.ID {
			return action.Result{}, errors.New("publication repository identity changed; inspect before retrying")
		}
	} else {
		saved.ID = repository.ID
		data, _ := json.Marshal(saved)
		if err := action.SaveCheckpoint(ctx, "publication-repository", data); err != nil {
			return action.Result{}, err
		}
	}
	refs, err := command.RemoteRefs(ctx, intent.Input.Repository, token)
	if err != nil {
		return action.Result{}, err
	}
	if len(refs) == 0 {
		pushErr := command.PushSnapshot(ctx, source, intent.Input.Repository, token)
		refs, err = command.RemoteRefs(ctx, intent.Input.Repository, token)
		if err != nil || refs["refs/heads/main"] != intent.CommitSHA {
			if pushErr != nil {
				return action.Result{}, fmt.Errorf("publication outcome may be uncertain; inspect the repository before a new publication: %w", pushErr)
			}
			if ctx.Err() != nil {
				return action.Result{}, fmt.Errorf("publication outcome may be uncertain; inspect the repository before a new publication: %w", ctx.Err())
			}
			return action.Result{}, errors.New("publication commit could not be confirmed; inspect the repository before a new publication")
		}
	}
	if refs["refs/heads/main"] != intent.CommitSHA {
		return action.Result{}, errors.New("repository already contains different history; choose a new or empty repository")
	}
	url := "https://github.com/" + repository.FullName
	return action.JSONResult(PublishAgentOutput{RepositoryURL: url, CommitURL: url + "/commit/" + intent.CommitSHA, CommitSHA: intent.CommitSHA, Visibility: intent.Input.Visibility, Branch: "main"})
}
