package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

type Preset struct {
	ID, Name, URL, KeyURL string
	KeyEnv                []string
}

func Presets() []Preset {
	return []Preset{
		{"openai", "OpenAI", "https://api.openai.com/v1", "https://platform.openai.com/api-keys", []string{"OPENAI_API_KEY"}},
		{"anthropic", "Anthropic", "https://api.anthropic.com/v1", "https://platform.claude.com/settings/keys", []string{"ANTHROPIC_API_KEY"}},
		{"gemini", "Google Gemini", "https://generativelanguage.googleapis.com/v1beta/openai", "https://aistudio.google.com/api-keys", []string{"GEMINI_API_KEY", "GOOGLE_API_KEY"}},
		{"xai", "xAI", "https://api.x.ai/v1", "https://console.x.ai/team/default/api-keys", []string{"XAI_API_KEY"}},
		{"deepseek", "DeepSeek", "https://api.deepseek.com/v1", "https://platform.deepseek.com/api_keys", []string{"DEEPSEEK_API_KEY"}},
		{"other", "Other (OpenAI-compatible)", "", "", []string{"COMMANDER_LLM_API_KEY"}},
	}
}

type Config struct {
	Provider, BaseURL, Model, APIKey string
	HTTPClient                       *http.Client
	Debug                            bool
}

type Client struct {
	config Config
	http   *http.Client
}

func New(config Config) (*Client, error) {
	if err := ValidateURL(config.BaseURL); err != nil {
		return nil, err
	}
	config.BaseURL = strings.TrimRight(config.BaseURL, "/")
	client := &http.Client{Timeout: 2 * time.Minute}
	if config.HTTPClient != nil {
		*client = *config.HTTPClient
	}
	// Credentials belong to this endpoint; never follow redirects with them.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{config: config, http: client}, nil
}

func ValidateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("enter an HTTP(S) base URL without credentials, query parameters or a fragment")
	}
	return nil
}

func (c *Client) Models(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var models []string
	path := "/models"
	for page := 0; page < 20; page++ {
		response, err := c.request(ctx, http.MethodGet, path, nil)
		if err != nil {
			return nil, err
		}
		var result struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
			HasMore bool   `json:"has_more"`
			LastID  string `json:"last_id"`
		}
		err = json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&result)
		response.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read models: %w", err)
		}
		for _, m := range result.Data {
			if m.ID != "" {
				models = append(models, m.ID)
			}
		}
		if !result.HasMore || result.LastID == "" {
			break
		}
		path = "/models?after_id=" + url.QueryEscape(result.LastID)
	}
	sort.Strings(models)
	return models, nil
}

func (c *Client) Complete(ctx context.Context, system string, history []Message, tools []Tool, delta func(string)) (Message, error) {
	if strings.TrimSpace(c.config.Model) == "" {
		return Message{}, errors.New("a model name is required")
	}
	if c.config.Provider == "anthropic" {
		return c.anthropic(ctx, system, history, tools, delta)
	}
	if c.config.Provider == "openai" {
		return c.responses(ctx, system, history, tools, delta)
	}
	return c.compatible(ctx, system, history, tools, delta)
}

func (c *Client) request(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.config.BaseURL+path, reader)
	if err != nil {
		return nil, errors.New("could not construct model request")
	}
	request.Header.Set("Content-Type", "application/json")
	if c.config.Provider == "anthropic" {
		request.Header.Set("anthropic-version", "2023-06-01")
		request.Header.Set("x-api-key", c.config.APIKey)
	} else if c.config.APIKey != "" {
		request.Header.Set("Authorization", "Bearer "+c.config.APIKey)
	}
	response, err := c.http.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, c.diagnosticError("model endpoint unavailable; check the URL and connection", []byte(err.Error()))
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()
		const limit = 64 * 1024
		detail, readErr := io.ReadAll(io.LimitReader(response.Body, limit+1))
		// Never show a partial body: it may end halfway through a credential.
		if readErr != nil {
			detail = []byte("could not read provider error body")
		} else if len(detail) > limit {
			detail = []byte("provider error body exceeds 64 KiB")
		}
		authentication := c.authenticationFailure(response.StatusCode, detail)
		hint := "check the endpoint and model name"
		if authentication {
			hint = "check your API key and model access"
		} else if response.StatusCode == http.StatusTooManyRequests {
			hint = "rate or quota limit reached; try again later"
		}
		summary := fmt.Sprintf("model API returned HTTP %d: %s", response.StatusCode, hint)
		err := c.diagnosticError(summary, detail)
		if authentication {
			return nil, &AuthenticationError{Err: err}
		}
		return nil, err
	}
	return response, nil
}
