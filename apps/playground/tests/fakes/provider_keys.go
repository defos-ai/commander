package fakes

import (
	"fmt"
	"io"
	"net/http"
)

type ProviderKeyCall struct {
	Provider, BaseURL, Value string
}

type ProviderKeyCache struct {
	value string
	err   error
	bound [2]string
	loads chan ProviderKeyCall
	saves chan ProviderKeyCall
}

func NewBoundProviderKeyCache(provider, baseURL, value string) *ProviderKeyCache {
	cache := NewProviderKeyCache(value, nil)
	cache.bound = [2]string{provider, baseURL}
	return cache
}

func NewProviderKeyCache(value string, err error) *ProviderKeyCache {
	return &ProviderKeyCache{value: value, err: err, loads: make(chan ProviderKeyCall, 16), saves: make(chan ProviderKeyCall, 16)}
}

func (c *ProviderKeyCache) Load(provider, baseURL string) (string, error) {
	c.loads <- ProviderKeyCall{Provider: provider, BaseURL: baseURL}
	if c.bound != [2]string{} && c.bound != [2]string{provider, baseURL} {
		return "", nil
	}
	return c.value, c.err
}

func (c *ProviderKeyCache) Save(provider, baseURL, value string) error {
	c.saves <- ProviderKeyCall{Provider: provider, BaseURL: baseURL, Value: value}
	return nil
}

func (c *ProviderKeyCache) Loads() []ProviderKeyCall { return providerKeyCalls(c.loads) }
func (c *ProviderKeyCache) Saves() []ProviderKeyCall { return providerKeyCalls(c.saves) }

func providerKeyCalls(source <-chan ProviderKeyCall) []ProviderKeyCall {
	var calls []ProviderKeyCall
	for {
		select {
		case call := <-source:
			calls = append(calls, call)
		default:
			return calls
		}
	}
}

type ProviderKeyRequest struct {
	Method, Path, Authorization, Body string
}

type ProviderKeyModel struct {
	accepted string
	requests chan ProviderKeyRequest
}

func NewProviderKeyModel(accepted string) *ProviderKeyModel {
	return &ProviderKeyModel{accepted: accepted, requests: make(chan ProviderKeyRequest, 16)}
}

func (m *ProviderKeyModel) Requests() []ProviderKeyRequest {
	var requests []ProviderKeyRequest
	for {
		select {
		case request := <-m.requests:
			requests = append(requests, request)
		default:
			return requests
		}
	}
}

func (m *ProviderKeyModel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	m.requests <- ProviderKeyRequest{Method: r.Method, Path: r.URL.Path, Authorization: r.Header.Get("Authorization"), Body: string(body)}
	if r.Header.Get("Authorization") != "Bearer "+m.accepted {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"message":"API key rejected"}}`)
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/custom/v1/models":
		fmt.Fprint(w, `{"data":[{"id":"fixture-model"}]}`)
	case r.Method == http.MethodPost && r.URL.Path == "/custom/v1/responses":
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"type":"response.output_text.delta","delta":"Provider key remains private"}

data: {"type":"response.completed","response":{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Provider key remains private"}]}]}}

`)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}
