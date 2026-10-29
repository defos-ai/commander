package models

import "time"

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

type QuestionResponse struct {
	ID         string     `json:"id"`
	Status     string     `json:"status"`
	StepID     string     `json:"step_id,omitempty"`
	TaskID     string     `json:"task_id,omitempty"`
	CommandID  string     `json:"command_id,omitempty"`
	Key        string     `json:"key,omitempty"`
	Text       string     `json:"text"`
	Options    []string   `json:"options,omitempty"`
	Answer     string     `json:"answer,omitempty"`
	AnsweredBy string     `json:"answered_by,omitempty"`
	AskedAt    time.Time  `json:"asked_at"`
	AnsweredAt *time.Time `json:"answered_at,omitempty"`
	FallbackAt time.Time  `json:"fallback_at"`
}

type QuestionListResponse struct {
	Items              []QuestionResponse `json:"items"`
	LastQuestion       *QuestionResponse  `json:"last_question"`
	RemainingQuestions int                `json:"remaining_questions"`
}
