package fakes

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

type PublicationState struct {
	Exists, Private bool
	ID              int64
}

type PublicationHTTP struct {
	token, repositoryName string
	state                 PublicationState
	requests, creates     int
}

func NewPublicationHTTP(token, repository string) *PublicationHTTP {
	return &PublicationHTTP{token: token, repositoryName: repository, state: PublicationState{ID: 42}}
}

func (f *PublicationHTTP) Snapshot() PublicationState { return f.state }
func (f *PublicationHTTP) Change(change func(*PublicationState)) {
	next := f.state
	change(&next)
	f.state = next
}
func (f *PublicationHTTP) RequestCount() int { return f.requests }
func (f *PublicationHTTP) CreateCount() int  { return f.creates }

func (f *PublicationHTTP) RoundTrip(request *http.Request) (*http.Response, error) {
	f.requests++
	if request.URL.Scheme != "https" || request.URL.Host != "api.github.com" || request.Header.Get("Authorization") != "Bearer "+f.token {
		return nil, errors.New("unexpected GitHub origin or authorization")
	}
	status, body := http.StatusOK, any(nil)
	switch request.Method + " " + request.URL.Path {
	case "GET /repos/" + f.repositoryName:
		if !f.state.Exists {
			status, body = http.StatusNotFound, map[string]any{"message": "Not found"}
		} else {
			body = f.repository()
		}
	case "GET /user":
		body = map[string]any{"login": "fixture"}
	case "POST /user/repos":
		var input struct {
			Name     string `json:"name"`
			Private  bool   `json:"private"`
			AutoInit bool   `json:"auto_init"`
		}
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil || input.Name != "built-agent" || input.AutoInit || f.state.Exists {
			return nil, errors.New("unexpected repository creation")
		}
		f.state.Exists, f.state.Private = true, input.Private
		f.creates++
		status, body = http.StatusCreated, f.repository()
	default:
		return nil, errors.New("unexpected GitHub request; visibility must never be changed")
	}
	data, _ := json.Marshal(body)
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(data)), Request: request}, nil
}

func (f *PublicationHTTP) repository() map[string]any {
	return map[string]any{"id": f.state.ID, "full_name": f.repositoryName, "private": f.state.Private, "default_branch": "main"}
}
