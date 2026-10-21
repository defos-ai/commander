package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var ErrNotFound = errors.New("GitHub repository was not found or the token cannot access it")

var ownerPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}$`)
var namePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)

// Client talks only to GitHub's API. Neither a task nor a repository response
// can choose where its authorization header is sent.
type Client struct {
	Token []byte
}

type Repository struct {
	ID            int64  `json:"id"`
	FullName      string `json:"full_name"`
	Private       bool   `json:"private"`
	DefaultBranch string `json:"default_branch"`
}

func parseRepository(repository string) (owner, name string, err error) {
	owner, name, found := strings.Cut(repository, "/")
	if !found || !ownerPattern.MatchString(owner) || !namePattern.MatchString(name) || name == "." || name == ".." || strings.HasSuffix(strings.ToLower(name), ".git") {
		return "", "", errors.New("repository must be a GitHub owner/name")
	}
	return owner, name, nil
}

func ValidateRepository(repository string) error {
	_, _, err := parseRepository(repository)
	return err
}

func (c Client) GetRepository(ctx context.Context, repository string) (Repository, error) {
	owner, name, err := parseRepository(repository)
	if err != nil {
		return Repository{}, err
	}
	var result Repository
	err = c.request(ctx, http.MethodGet, "/repos/"+url.PathEscape(owner)+"/"+url.PathEscape(name), nil, &result)
	if err != nil {
		return Repository{}, err
	}
	return checkedRepository(result, repository)
}

func (c Client) CreateRepository(ctx context.Context, repository, visibility string) (Repository, error) {
	owner, name, err := parseRepository(repository)
	if err != nil {
		return Repository{}, err
	}
	if visibility != "public" && visibility != "private" {
		return Repository{}, errors.New("repository visibility must be public or private")
	}
	var user struct {
		Login string `json:"login"`
	}
	if err := c.request(ctx, http.MethodGet, "/user", nil, &user); err != nil {
		return Repository{}, err
	}
	if !ownerPattern.MatchString(user.Login) {
		return Repository{}, errors.New("GitHub returned an invalid authenticated user")
	}
	path := "/user/repos"
	if !strings.EqualFold(owner, user.Login) {
		path = "/orgs/" + url.PathEscape(owner) + "/repos"
	}
	body := struct {
		Name     string `json:"name"`
		Private  bool   `json:"private"`
		AutoInit bool   `json:"auto_init"`
	}{Name: name, Private: visibility == "private", AutoInit: false}
	var result Repository
	if err := c.request(ctx, http.MethodPost, path, body, &result); err != nil {
		return Repository{}, err
	}
	result, err = checkedRepository(result, repository)
	if err == nil && result.Private != body.Private {
		err = errors.New("GitHub repository visibility does not match the requested visibility")
	}
	return result, err
}

func checkedRepository(result Repository, repository string) (Repository, error) {
	if result.ID <= 0 || !strings.EqualFold(result.FullName, repository) {
		return Repository{}, errors.New("GitHub returned an unexpected repository")
	}
	return result, nil
}

func (c Client) request(ctx context.Context, method, path string, body, result any) error {
	if len(c.Token) == 0 {
		return errors.New("GitHub token is unavailable")
	}
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return errors.New("cannot encode GitHub request")
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, "https://api.github.com"+path, reader)
	if err != nil {
		return errors.New("cannot prepare GitHub request")
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("Authorization", "Bearer "+string(c.Token))
	request.Header.Set("X-GitHub-Api-Version", "2026-03-10")
	request.Header.Set("User-Agent", "Commander-Builder")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	client := http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Transport and server errors can contain authorization or remote data.
		return errors.New("GitHub request failed; check connectivity and token permissions")
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusCreated {
		return fmt.Errorf("GitHub request failed (HTTP %d); check repository access, visibility and token permissions", response.StatusCode)
	}
	const maxResponse = 1 << 20
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponse+1))
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil || len(data) > maxResponse {
		return errors.New("cannot read bounded GitHub response")
	}
	if err := json.Unmarshal(data, result); err != nil {
		return errors.New("GitHub returned an invalid response")
	}
	return nil
}
