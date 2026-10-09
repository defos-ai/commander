package actions

import (
	"encoding/json"

	"github.com/defos-ai/commander/framework/agent/action"
)

const (
	CheckAccessibilityName = "check-accessibility"
	CapturePageName        = "capture-page"
)

func NewActions() []action.Executor {
	return []action.Executor{NewCheckAccessibility(), NewCapturePage()}
}

type PageInput struct {
	URL string `json:"url"`
}

func (in PageInput) Validate() error { _, err := parseURL(in.URL); return err }

const pageInputSchema = `{"type":"object","properties":{"url":{"type":"string","minLength":1,"maxLength":2048,"description":"Absolute HTTP(S) page URL"}},"required":["url"],"additionalProperties":false}`

func outputSchema[T any]() json.RawMessage {
	schema, err := action.SchemaFor[T]()
	if err != nil {
		panic(err)
	}
	return schema
}
