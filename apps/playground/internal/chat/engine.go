package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/defos-ai/commander/apps/playground/internal/llm"
	bridge "github.com/defos-ai/commander/apps/playground/internal/mcp"
)

type Model interface {
	Complete(context.Context, string, []llm.Message, []llm.Tool, func(string)) (llm.Message, error)
}
type Commander interface {
	Tools() []llm.Tool
	Instructions() string
	Call(context.Context, string, json.RawMessage) (bridge.Result, error)
	Operation(context.Context, string) (bridge.Operation, error)
}

type Event struct {
	Kind, Text, ID          string
	CallID                  string
	OperationID, QuestionID string
	Answer                  string
	Operation               *bridge.Operation
}
type answerRequest struct{ operationID, questionID, answer string }
type job struct {
	text, operationID string
	reviews           []reviewKey
}
type turnResult struct {
	history []llm.Message
	err     error
}

type Engine struct {
	ctx           context.Context
	cancel        context.CancelFunc
	model         Model
	commander     Commander
	input         chan string
	events        chan Event
	done          chan struct{}
	watchRequests chan string
	updates       chan bridge.Operation
	answers       chan answerRequest
	wg            sync.WaitGroup
}

func New(ctx context.Context, model Model, commander Commander) *Engine {
	ctx, cancel := context.WithCancel(ctx)
	e := &Engine{ctx: ctx, cancel: cancel, model: model, commander: commander, input: make(chan string, 64), events: make(chan Event, 256), done: make(chan struct{}), watchRequests: make(chan string, 32), updates: make(chan bridge.Operation, 32), answers: make(chan answerRequest, 16)}
	go e.run()
	return e
}
func (e *Engine) Events() <-chan Event { return e.events }
func (e *Engine) Close()               { e.cancel(); <-e.done }
func (e *Engine) Send(text string) error {
	select {
	case <-e.ctx.Done():
		return e.ctx.Err()
	case e.input <- text:
		return nil
	default:
		return errors.New("message queue is full; wait for the current reply")
	}
}

// AnswerQuestion accepts explicit operator input without parsing command text or
// sending the choice to the model first. Completion arrives as answer_end.
func (e *Engine) AnswerQuestion(operationID, questionID, answer string) error {
	if strings.TrimSpace(operationID) == "" || strings.TrimSpace(questionID) == "" || strings.TrimSpace(answer) == "" {
		return errors.New("an operation, question and non-empty answer are required")
	}
	select {
	case <-e.ctx.Done():
		return e.ctx.Err()
	case e.answers <- answerRequest{operationID, questionID, answer}:
		return nil
	default:
		return errors.New("answer queue is full; wait for the current request")
	}
}
func (e *Engine) emit(event Event) {
	select {
	case e.events <- event:
	case <-e.ctx.Done():
	}
}

func (e *Engine) run() {
	defer close(e.done)
	defer close(e.events)
	defer e.wg.Wait()
	defer e.cancel()
	var history []llm.Message
	var pending []job
	var automatic []job
	var cancelTurn context.CancelFunc
	completed := make(chan turnResult, 1)
	watched := map[string]bool{}
	operations := map[string]bridge.Operation{}
	seen := map[reviewKey]bool{}
	paused := false
	watch := func(id string) {
		if id == "" || watched[id] {
			return
		}
		watched[id] = true
		e.wg.Add(1)
		go func() { defer e.wg.Done(); e.watch(id) }()
	}
	for {
		if cancelTurn == nil && (len(pending) > 0 || len(automatic) > 0) {
			var next job
			if len(pending) > 0 {
				next, pending = pending[0], pending[1:]
			} else {
				next, automatic = automatic[0], automatic[1:]
			}
			ctx, cancel := context.WithCancel(e.ctx)
			cancelTurn = cancel
			e.emit(Event{Kind: "busy", Text: "Thinking"})
			e.wg.Add(1)
			go func(previous []llm.Message, item job) {
				defer e.wg.Done()
				updated, err := e.turn(ctx, previous, item)
				completed <- turnResult{updated, err}
			}(append([]llm.Message(nil), history...), next)
		}
		select {
		case <-e.ctx.Done():
			return
		case result := <-completed:
			cancelTurn()
			cancelTurn = nil
			history = result.history
			if result.err != nil && !errors.Is(result.err, context.Canceled) {
				e.emit(Event{Kind: "error", Text: result.err.Error()})
			}
			e.emit(Event{Kind: "idle"})
		case id := <-e.watchRequests:
			watch(id)
		case answer := <-e.answers:
			paused = false
			e.submitAnswer(answer, operations)
		case text := <-e.input:
			text = strings.TrimSpace(text)
			if text == "" {
				continue
			}
			fields := strings.Fields(text)
			switch {
			case text == "/stop":
				paused = true
				notice := ""
				if cancelTurn != nil {
					cancelTurn()
					notice = "Reply stopped."
				}
				pending, automatic = nil, nil
				for id := range watched {
					if operation, known := operations[id]; !known || !operation.Settled() {
						notice = strings.TrimSpace(notice + " Model review paused; /watch <id> resumes it. Operation updates remain visible.")
						break
					}
				}
				if notice == "" {
					notice = "No reply is running."
				}
				e.emit(Event{Kind: "notice", Text: notice})
			case fields[0] == "/help":
				if len(fields) == 1 {
					e.emit(Event{Kind: "notice", Text: Help})
				} else {
					e.emit(Event{Kind: "error", Text: "Usage: /help"})
				}
			case text == "/clear":
				if cancelTurn != nil {
					e.emit(Event{Kind: "error", Text: "Use /stop before clearing the conversation."})
					continue
				}
				history = nil
				e.emit(Event{Kind: "notice", Text: "Model conversation cleared. Saved operations are unchanged."})
			case text == "/status":
				e.emit(Event{Kind: "notice", Text: fmt.Sprintf("MCP connected · watching %d operations · %d queued messages", len(operations), len(pending))})
			case text == "/operations":
				e.control("list_operations", map[string]any{})
			case strings.HasPrefix(text, "/watch "):
				paused = false
				id := strings.TrimSpace(strings.TrimPrefix(text, "/watch "))
				for key := range seen {
					if key.operationID == id {
						delete(seen, key)
					}
				}
				watch(id)
				if operation, ok := operations[id]; ok {
					automatic = append(automatic, decisions(operation, seen)...)
				}
				e.emit(Event{Kind: "notice", Text: "Watching operation " + id})
			case strings.HasPrefix(text, "/run "):
				e.runAction(strings.TrimPrefix(text, "/run "))
			case strings.HasPrefix(text, "/cancel "):
				e.control("cancel_operation", map[string]any{"operation_id": strings.TrimSpace(strings.TrimPrefix(text, "/cancel "))})
			case strings.HasPrefix(text, "/answer "):
				paused = false
				e.answer(strings.TrimPrefix(text, "/answer "), operations)
			case strings.HasPrefix(text, "/"):
				e.emit(Event{Kind: "error", Text: "Unknown command. Type /help."})
			default:
				paused = false
				if len(pending) >= 32 {
					e.emit(Event{Kind: "error", Text: "Too many queued messages; wait for a reply."})
					continue
				}
				pending = append(pending, job{text: text})
				if cancelTurn != nil {
					e.emit(Event{Kind: "notice", Text: "Message queued; you can keep typing."})
				}
			}
		case operation := <-e.updates:
			if previous, ok := operations[operation.ID]; ok && operation.UpdatedAt.Before(previous.UpdatedAt) {
				continue
			}
			if _, known := operations[operation.ID]; !known {
				// Opening a saved operation should not replay old operator answers.
				for _, question := range operation.Questions {
					if question.Status == bridge.QuestionAnswered {
						seen[reviewKey{operationID: operation.ID, questionID: question.ID, status: string(question.Status)}] = true
					}
				}
			}
			operations[operation.ID] = operation
			e.emit(Event{Kind: "operation", ID: operation.ID, Operation: &operation})
			if !paused {
				automatic = append(automatic, decisions(operation, seen)...)
			}
		}
	}
}

const Help = `/admin [command] — Admin UI and commands
/credentials — Show API login and MCP token (all-in-one)
/secrets — Provide secrets needed by watched operations
/operations — List saved operations
/watch <id> — Follow work and resume review
/stop — Stop the reply and clear queued messages
/clear — Clear conversation; keep saved operations
/exit — Exit Playground

Docs: ` + HelpDocsURL

const HelpDocsURL = "https://github.com/defos-ai/commander/blob/main/apps/playground/README.md#chat"
