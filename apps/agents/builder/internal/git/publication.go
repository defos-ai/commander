package git

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/defos-ai/commander/apps/agents/builder/internal/github"
)

// CommitSnapshot starts fresh history for the verified source attachment. Stable
// metadata makes a retried publication produce the same commit, even after the
// temporary working directory has gone away.
func (g Command) CommitSnapshot(ctx context.Context, directory string, when time.Time, correlation string) (string, error) {
	if when.IsZero() || when.Unix() <= 0 || correlation == "" || len(correlation) > 256 || strings.ContainsAny(correlation, "\r\n\x00") {
		return "", errors.New("publication commit metadata is invalid")
	}
	if _, err := os.Lstat(filepath.Join(directory, ".git")); !errors.Is(err, os.ErrNotExist) {
		return "", errors.New("publication snapshot must not contain Git history")
	}
	if _, err := g.Run(ctx, directory, "init", "--quiet", "--initial-branch=main", "--object-format=sha1", "--template="); err != nil {
		return "", fmt.Errorf("initialize publication snapshot: %w", err)
	}
	if _, err := g.Run(ctx, directory, "-c", "core.autocrlf=false", "-c", "core.filemode=true", "add", "--all", "--force", "--", "."); err != nil {
		return "", fmt.Errorf("stage publication snapshot: %w", err)
	}
	date := when.UTC().Format(time.RFC3339)
	env := []string{
		"GIT_AUTHOR_NAME=Commander", "GIT_AUTHOR_EMAIL=commander@defos.ai", "GIT_AUTHOR_DATE=" + date,
		"GIT_COMMITTER_NAME=Commander", "GIT_COMMITTER_EMAIL=commander@defos.ai", "GIT_COMMITTER_DATE=" + date,
	}
	message := "Build agent with Commander\n\nPublication: " + correlation
	if _, err := g.run(ctx, directory, env, "-c", "commit.gpgsign=false", "commit", "--quiet", "--no-verify", "--no-gpg-sign", "-m", message); err != nil {
		return "", fmt.Errorf("commit publication snapshot: %w", err)
	}
	result, err := g.Run(ctx, directory, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return "", fmt.Errorf("read publication commit: %w", err)
	}
	sha := strings.TrimSpace(result)
	if !commitSHA(sha) {
		return "", errors.New("Git returned an invalid publication commit")
	}
	return sha, nil
}

// RemoteRefs checks every advertised reference so a repository with no main
// branch but existing history is never mistaken for an empty repository.
func (g Command) RemoteRefs(ctx context.Context, repository string, token []byte) (map[string]string, error) {
	env, remote, err := publicationRemote(repository, token)
	if err != nil {
		return nil, err
	}
	output, err := g.run(ctx, g.Home, env, "ls-remote", "--", remote)
	if err != nil {
		return nil, fmt.Errorf("inspect publication repository: %w", err)
	}
	refs := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if line == "" {
			continue
		}
		sha, ref, ok := strings.Cut(line, "\t")
		if !ok || !commitSHA(sha) || ref != "HEAD" && !strings.HasPrefix(ref, "refs/") || strings.ContainsAny(ref, "\r\n\x00 ") {
			return nil, errors.New("Git returned invalid repository references")
		}
		refs[ref] = sha
	}
	return refs, nil
}

func (g Command) PushSnapshot(ctx context.Context, directory, repository string, token []byte) error {
	env, remote, err := publicationRemote(repository, token)
	if err != nil {
		return err
	}
	// Use the fixed destination directly, without saving credentials/remotes to
	// disk. A normal push cannot overwrite a conflicting initial publication.
	_, err = g.run(ctx, directory, env, "push", "--quiet", "--no-verify", "--", remote, "HEAD:refs/heads/main")
	if err != nil {
		return fmt.Errorf("push publication snapshot: %w", err)
	}
	return nil
}

func publicationRemote(repository string, token []byte) ([]string, string, error) {
	if err := github.ValidateRepository(repository); err != nil {
		return nil, "", err
	}
	if len(token) == 0 {
		return nil, "", errors.New("GitHub token is unavailable")
	}
	credentials := append([]byte("x-access-token:"), token...)
	authorization := base64.StdEncoding.EncodeToString(credentials)
	clear(credentials)
	env := []string{
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=http.https://github.com/.extraHeader",
		"GIT_CONFIG_VALUE_0=Authorization: Basic " + authorization,
	}
	return env, "https://github.com/" + repository + ".git", nil
}

func commitSHA(sha string) bool {
	decoded, err := hex.DecodeString(sha)
	return err == nil && (len(decoded) == 20 || len(decoded) == 32)
}
