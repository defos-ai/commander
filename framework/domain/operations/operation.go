package operations

import (
	"encoding/json"
	"time"

	"github.com/defos-ai/commander/framework/domain/missions"
)

type Status string

const (
	Planning  Status = "planning"
	Running   Status = "running"
	Waiting   Status = "waiting"
	Completed Status = "completed"
	Failed    Status = "failed"
	Cancelled Status = "cancelled"
)

func (s Status) Terminal() bool { return s == Completed || s == Failed || s == Cancelled }

type Operation struct {
	ID                 string           `json:"id"`
	Mission            missions.Mission `json:"mission"`
	Input              json.RawMessage  `json:"input"`
	Status             Status           `json:"status"`
	Preflight          *Preflight       `json:"preflight,omitempty"`
	Tasks              []Task           `json:"tasks"`
	Questions          []Question       `json:"questions"`
	Output             json.RawMessage  `json:"output,omitempty"`
	ReportID           string           `json:"report_id,omitempty"`
	FinalizationTaskID string           `json:"finalization_task_id,omitempty"`
	Error              string           `json:"error,omitempty"`
	Events             []Event          `json:"events"`
	CreatedAt          time.Time        `json:"created_at"`
	UpdatedAt          time.Time        `json:"updated_at"`
}

func (o Operation) Clone() Operation {
	if o.Preflight != nil {
		preflight := o.Preflight.Clone()
		o.Preflight = &preflight
	}
	o.Mission = o.Mission.Clone()
	o.Input = append(json.RawMessage(nil), o.Input...)
	o.Output = append(json.RawMessage(nil), o.Output...)
	o.Tasks = append([]Task{}, o.Tasks...)
	for i := range o.Tasks {
		o.Tasks[i] = o.Tasks[i].Clone()
	}
	o.Questions = append([]Question{}, o.Questions...)
	for i := range o.Questions {
		o.Questions[i] = o.Questions[i].Clone()
	}
	o.Events = append([]Event{}, o.Events...)
	return o
}
