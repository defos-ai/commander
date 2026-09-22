package actions

import (
	"encoding/json"

	"commander/framework/agent/action"
)

const (
	DiscoverPagesName      = "discover-pages"
	CheckLinksName         = "check-links"
	CheckAccessibilityName = "check-accessibility"
	CapturePageName        = "capture-page"
	CheckSEOName           = "check-seo"
	DetectTechnologiesName = "detect-technologies"
)

func NewActions() []action.Executor {
	return []action.Executor{NewDiscoverPages(), NewCheckLinks(), NewCheckAccessibility(), NewCapturePage(), NewCheckSEO(), NewDetectTechnologies()}
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
