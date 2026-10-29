package agent

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/defos-ai/commander/framework/agent/action"
	agentevents "github.com/defos-ai/commander/framework/comms/clients/agent/events"
	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/operations"
)

const questionRetryInterval = 500 * time.Millisecond

type questionClientKey struct{}

type questionClient struct {
	agentID string
	service context.Context
	events  controllerevents.Client
	mu      sync.Mutex
	pending map[string]questionWait
}

type questionWait struct {
	message messages.Message
	request controllerevents.QuestionRequest
	reply   chan agentevents.QuestionResponse
}

type executionQuestions struct {
	client    *questionClient
	lifetime  context.Context
	commandID string
	work      messages.WorkContext
	deadline  time.Time
}

func (r *executionQuestions) Question(ctx context.Context, key, text string, options []string) (operations.Question, error) {
	if !r.deadline.IsZero() {
		var done context.CancelFunc
		ctx, done = context.WithDeadline(ctx, r.deadline)
		defer done()
	}
	if err := r.contextError(ctx); err != nil {
		return operations.Question{}, err
	}
	caller := ctx
	ctx, cancel := context.WithCancel(ctx)
	stopExecution := context.AfterFunc(r.lifetime, cancel)
	stopService := context.AfterFunc(r.client.service, cancel)
	defer func() { stopExecution(); stopService(); cancel() }()
	request := controllerevents.QuestionRequest{CommandID: r.commandID, Key: key, Text: text, Options: slices.Clone(options)}
	message, err := controllerevents.NewQuestionRequest(r.client.agentID, r.work, request)
	if err != nil {
		return operations.Question{}, action.ErrQuestionsUnavailable
	}
	wait := questionWait{message: message, request: request, reply: make(chan agentevents.QuestionResponse, 1)}
	r.client.mu.Lock()
	r.client.pending[message.ID] = wait
	r.client.mu.Unlock()
	defer func() {
		r.client.mu.Lock()
		delete(r.client.pending, message.ID)
		r.client.mu.Unlock()
	}()
	ticker := time.NewTicker(questionRetryInterval)
	defer ticker.Stop()
	publish := func() error {
		if err := r.contextError(caller); err != nil {
			return err
		}
		publication, done := context.WithTimeout(ctx, 5*time.Second)
		// The key makes requests idempotent. A lost receipt retries this question,
		// never the action or its external effects.
		_ = r.client.events.Publish(publication, message)
		done()
		return nil
	}
	if err := publish(); err != nil {
		return operations.Question{}, err
	}
	for {
		select {
		case <-ctx.Done():
			return operations.Question{}, r.contextError(caller)
		case reply := <-wait.reply:
			if err := r.contextError(caller); err != nil {
				return operations.Question{}, err
			}
			switch reply.Error {
			case agentevents.QuestionConflict:
				return operations.Question{}, action.ErrQuestionConflict
			case agentevents.QuestionInactive:
				return operations.Question{}, action.ErrQuestionsUnavailable
			case "":
				if reply.Question.Status == operations.QuestionAnswered {
					return reply.Question.Clone(), nil
				}
			default:
				return operations.Question{}, action.ErrQuestionRejected
			}
		case <-ticker.C:
			if err := publish(); err != nil {
				return operations.Question{}, err
			}
		}
	}
}

func (r *executionQuestions) contextError(ctx context.Context) error {
	if err := requestCanceled(ctx, r.lifetime, r.client.service); err != nil {
		return err
	}
	if !r.deadline.IsZero() && !time.Now().Before(r.deadline) {
		return context.DeadlineExceeded
	}
	return nil
}

func (c *questionClient) receive(message messages.Message) {
	if message.AgentID != c.agentID {
		return
	}
	response, err := agentevents.DecodeQuestionResponse(message)
	if err != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	wait, exists := c.pending[message.CorrelationID]
	if !exists || response.CommandID != wait.request.CommandID || response.Key != wait.request.Key || message.Work != wait.message.Work {
		return
	}
	if response.Question != nil {
		if response.Question.Text != wait.request.Text || !slices.Equal(response.Question.Options, wait.request.Options) {
			return
		}
		cloned := response.Question.Clone()
		response.Question = &cloned
	}
	select {
	case wait.reply <- response:
	default:
	}
}

var _ action.QuestionProvider = (*executionQuestions)(nil)
