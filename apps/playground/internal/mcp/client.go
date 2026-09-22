// Package mcp connects the playground to Commander's public MCP interface.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"commander/apps/playground/internal/llm"
	protocol "github.com/modelcontextprotocol/go-sdk/mcp"
)

type Client struct {
	session      *protocol.ClientSession
	tools        []llm.Tool
	instructions string
	stop         func() bool
}

func Connect(ctx context.Context, endpoint string) (*Client, error) {
	if err := llm.ValidateURL(endpoint); err != nil {
		return nil, err
	}
	lifetime := ctx
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := protocol.NewClient(&protocol.Implementation{Name: "commander-playground", Version: "0.1.0"}, &protocol.ClientOptions{Logger: logger})
	httpClient := &http.Client{Timeout: 2 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	session, err := client.Connect(ctx, &protocol.StreamableClientTransport{Endpoint: endpoint, HTTPClient: httpClient, DisableStandaloneSSE: true}, nil)
	if err != nil {
		return nil, errors.New("cannot connect to Commander MCP; check the URL and that the server is running")
	}
	result := &Client{session: session, instructions: session.InitializeResult().Instructions}
	cursor := ""
	for {
		page, err := session.ListTools(ctx, &protocol.ListToolsParams{Cursor: cursor})
		if err != nil {
			session.Close()
			return nil, errors.New("could not discover Commander MCP tools")
		}
		for _, tool := range page.Tools {
			// Operator answers come from the UI. The watcher owns long polling.
			if tool.Name == "answer_question" || tool.Name == "operation_events" {
				continue
			}
			schema, err := json.Marshal(tool.InputSchema)
			if err != nil {
				session.Close()
				return nil, err
			}
			result.tools = append(result.tools, llm.Tool{Name: tool.Name, Description: tool.Description, Schema: schema})
		}
		if page.NextCursor == "" {
			break
		}
		if page.NextCursor == cursor {
			session.Close()
			return nil, errors.New("MCP returned a repeated tool cursor")
		}
		cursor = page.NextCursor
	}
	result.tools = append(result.tools, llm.Tool{Name: "read_artifact", Description: "Read a missions://artifacts/ URI returned by Commander. Large results are bounded; do not invent missing content.", Schema: json.RawMessage(`{"type":"object","properties":{"uri":{"type":"string"}},"required":["uri"],"additionalProperties":false}`)})
	result.stop = context.AfterFunc(lifetime, func() { _ = session.Close() })
	return result, nil
}

func (c *Client) Close() error {
	if c.stop != nil {
		c.stop()
	}
	return c.session.Close()
}
func (c *Client) Tools() []llm.Tool    { return append([]llm.Tool(nil), c.tools...) }
func (c *Client) Instructions() string { return c.instructions }

type Result struct {
	Data    json.RawMessage
	Text    string
	IsError bool
}

func (c *Client) Call(ctx context.Context, name string, arguments json.RawMessage) (Result, error) {
	var input map[string]any
	if err := json.Unmarshal(arguments, &input); err != nil || input == nil {
		return Result{}, errors.New("tool arguments must be a JSON object")
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	if name == "read_artifact" {
		uri, _ := input["uri"].(string)
		if !strings.HasPrefix(uri, "missions://artifacts/") {
			return Result{}, errors.New("expected a Commander artifact URI")
		}
		resource, err := c.session.ReadResource(ctx, &protocol.ReadResourceParams{URI: uri})
		if err != nil {
			return Result{}, err
		}
		var content []map[string]any
		for _, item := range resource.Contents {
			entry := map[string]any{"uri": item.URI, "media_type": item.MIMEType}
			if item.Blob != nil {
				entry["binary_bytes"] = len(item.Blob)
			} else {
				entry["text"] = item.Text
			}
			content = append(content, entry)
		}
		data, err := json.Marshal(content)
		return Result{Data: data, Text: Bounded(data)}, err
	}
	result, err := c.session.CallTool(ctx, &protocol.CallToolParams{Name: name, Arguments: input})
	if err != nil {
		return Result{}, err
	}
	var data []byte
	if result.StructuredContent != nil {
		data, err = json.Marshal(result.StructuredContent)
	} else {
		for _, block := range result.Content {
			if text, ok := block.(*protocol.TextContent); ok {
				if len(data) != 0 {
					data = append(data, '\n')
				}
				data = append(data, text.Text...)
			}
		}
	}
	if err != nil {
		return Result{}, err
	}
	return Result{Data: data, Text: Bounded(data), IsError: result.IsError}, nil
}

func Bounded(data []byte) string {
	const limit = 96 * 1024
	if len(data) <= limit {
		return string(data)
	}
	preview, _ := json.Marshal(map[string]any{"truncated": true, "total_bytes": len(data), "preview": string(data[:limit])})
	return string(preview)
}

type Operation struct {
	ID                 string            `json:"id"`
	Status             string            `json:"status"`
	Tasks              []Task            `json:"tasks"`
	Questions          []Question        `json:"questions"`
	LastQuestion       *Question         `json:"last_question"`
	RemainingQuestions int               `json:"remaining_questions"`
	Events             []json.RawMessage `json:"events"`
	Version            uint64            `json:"version"`
	Raw                json.RawMessage   `json:"-"`
}
type Task struct {
	ID           string `json:"id"`
	Status       string `json:"status"`
	ActionName   string `json:"action_name"`
	Continuation *struct {
		Status     string `json:"status"`
		DecisionBy string `json:"decision_by"`
		Version    uint64 `json:"version"`
	} `json:"continuation"`
}
type QuestionStatus string

const (
	QuestionPending  QuestionStatus = "pending"
	QuestionAnswered QuestionStatus = "answered"
	QuestionExpired  QuestionStatus = "expired"
)

type Question struct {
	ID         string         `json:"id"`
	Status     QuestionStatus `json:"status"`
	Text       string         `json:"text"`
	Options    []string       `json:"options"`
	Answer     string         `json:"answer"`
	AskedAt    time.Time      `json:"asked_at"`
	ExpiresAt  time.Time      `json:"expires_at"`
	AnsweredAt *string        `json:"answered_at"`
}

func (q Question) Pending(now time.Time) bool {
	return q.Status == QuestionPending && (q.ExpiresAt.IsZero() || now.Before(q.ExpiresAt))
}

func (o Operation) Terminal() bool {
	return o.Status == "completed" || o.Status == "failed" || o.Status == "cancelled"
}
func (o Operation) Settled() bool {
	if !o.Terminal() {
		return false
	}
	for _, task := range o.Tasks {
		if task.Status == "running" {
			return false
		}
	}
	return true
}
func (c *Client) Operation(ctx context.Context, id string) (Operation, error) {
	args, _ := json.Marshal(map[string]string{"operation_id": id})
	result, err := c.Call(ctx, "get_operation", args)
	if err != nil {
		return Operation{}, err
	}
	if result.IsError {
		return Operation{}, fmt.Errorf("get_operation: %s", result.Text)
	}
	var operation Operation
	if err := json.Unmarshal(result.Data, &operation); err != nil {
		return Operation{}, err
	}
	if operation.ID != id {
		return Operation{}, errors.New("MCP returned a different operation")
	}
	operation.Raw = result.Data
	return operation, nil
}
