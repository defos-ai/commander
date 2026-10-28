package executions

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/defos-ai/commander/framework/domain"
	"github.com/defos-ai/commander/framework/id"
)

type LogQuery struct {
	OperationID string
	TaskID      string
	AgentID     string
	ActionName  string
	AfterLogID  string
	Limit       int
}

func (q LogQuery) Validate() error {
	if err := domain.ValidatePageLimit(q.Limit); err != nil {
		return err
	}
	if q.OperationID != "" && !id.Valid(q.OperationID) {
		return fmt.Errorf("%w: log operation ID must be a canonical UUID", domain.ErrInvalidInput)
	}
	if q.TaskID != "" && !id.Valid(q.TaskID) {
		return fmt.Errorf("%w: log task ID must be a canonical UUID", domain.ErrInvalidInput)
	}
	if q.AgentID != "" && !id.ValidAgent(q.AgentID) {
		return fmt.Errorf("%w: log agent ID must be a valid agent runtime ID", domain.ErrInvalidInput)
	}
	if q.ActionName != "" && (!utf8.ValidString(q.ActionName) || strings.TrimSpace(q.ActionName) == "") {
		return fmt.Errorf("%w: log action name must contain nonblank UTF-8 text", domain.ErrInvalidInput)
	}
	if q.AfterLogID != "" && !id.Valid(q.AfterLogID) {
		return fmt.Errorf("%w: log cursor must be a canonical UUID", domain.ErrInvalidInput)
	}
	return nil
}

func (q LogQuery) Matches(log Log) bool {
	return (q.OperationID == "" || q.OperationID == log.Work.OperationID) &&
		(q.TaskID == "" || q.TaskID == log.Work.TaskID) &&
		(q.AgentID == "" || q.AgentID == log.AgentID) &&
		(q.ActionName == "" || q.ActionName == log.ActionName)
}
