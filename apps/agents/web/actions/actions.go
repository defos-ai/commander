package actions

import (
	"encoding/json"

	"github.com/defos-ai/commander/framework/agent/action"
)

const (
	DiscoverPagesName          = "discover-pages"
	DiscoverPagesNoBrowserName = "discover-pages-no-browser"
	CheckLinksName             = "check-links"
	CheckSEOName               = "check-seo"
	DetectTechnologiesName     = "detect-technologies"
)

func NewActions() []action.Executor {
	return []action.Executor{NewDiscoverPages(), NewDiscoverPagesNoBrowser(), NewCheckLinks(), NewCheckSEO(), NewDetectTechnologies()}
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
