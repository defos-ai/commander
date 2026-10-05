package models

type AskQuestionRequest struct {
	TaskID         string   `json:"task_id,omitempty"`
	Version        uint64   `json:"version,omitempty"`
	StepID         string   `json:"step_id,omitempty"`
	Text           string   `json:"text"`
	Options        []string `json:"options,omitempty"`
	TimeoutSeconds int      `json:"timeout_seconds,omitempty"`
}

type AnswerQuestionRequest struct {
	Answer string `json:"answer"`
}
