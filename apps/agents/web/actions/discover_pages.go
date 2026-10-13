package actions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/defos-ai/commander/framework/agent/action"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
)

const (
	checkAccessibilityName = "check-accessibility"
	capturePageName        = "capture-page"
)

type DiscoverPagesInput struct {
	URL      string `json:"url"`
	MaxPages int    `json:"max_pages,omitempty"`
}
type Page struct {
	URL        string `json:"url"`
	Title      string `json:"title"`
	StatusCode int    `json:"status_code"`
}
type DiscoverPagesOutput struct {
	URL                  string   `json:"url"`
	Pages                []Page   `json:"pages"`
	MaxPages             int      `json:"max_pages"`
	Truncated            bool     `json:"truncated"`
	SkippedExternalLinks int      `json:"skipped_external_links"`
	SkippedNonHTMLPages  int      `json:"skipped_non_html_pages"`
	Errors               []string `json:"errors,omitempty"`
}

type DiscoverPages struct {
	*action.Base
	checks []string
}

func NewDiscoverPages() *DiscoverPages {
	return newDiscoverPages(DiscoverPagesName, "Discover up to ten HTML pages on the same hostname, including HTTP(S) and port changes. Propose link, accessibility, screenshot, SEO and technology checks for each. Excludes other hostnames and subdomains; does not submit forms.", []string{CheckLinksName, checkAccessibilityName, capturePageName, CheckSEOName, DetectTechnologiesName})
}

func NewDiscoverPagesNoBrowser() *DiscoverPages {
	return newDiscoverPages(DiscoverPagesNoBrowserName, "Discover up to ten HTML pages on the same hostname, including HTTP(S) and port changes. Propose link, SEO and technology checks without a browser. Excludes other hostnames and subdomains; does not submit forms.", []string{CheckLinksName, CheckSEOName, DetectTechnologiesName})
}

func newDiscoverPages(name, description string, checks []string) *DiscoverPages {
	return &DiscoverPages{
		Base:   action.NewBase(actionmodel.Action{Name: name, Description: description, InputSchema: json.RawMessage(`{"type":"object","properties":{"url":{"type":"string","minLength":1,"maxLength":2048},"max_pages":{"type":"integer","minimum":1,"maximum":10,"default":3}},"required":["url"],"additionalProperties":false}`), OutputSchema: outputSchema[DiscoverPagesOutput]()}),
		checks: checks,
	}
}
func (a *DiscoverPages) Run(ctx context.Context, raw json.RawMessage) (action.Result, error) {
	if err := a.Check(); err != nil {
		return action.Result{}, err
	}
	in := DiscoverPagesInput{MaxPages: 3}
	if err := action.DecodeInput(raw, &in); err != nil {
		return action.Result{}, err
	}
	if in.MaxPages < 1 || in.MaxPages > 10 {
		return action.Result{}, errors.New("max_pages must be between 1 and 10")
	}
	target, err := parseURL(in.URL)
	if err != nil {
		return action.Result{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	client, err := httpClient(ctx, target)
	if err != nil {
		return action.Result{}, err
	}
	defer client.CloseIdleConnections()
	out := DiscoverPagesOutput{URL: target.String(), MaxPages: in.MaxPages, Pages: []Page{}}
	queue := []string{target.String()}
	seen := map[string]bool{target.String(): true}
	visited := map[string]bool{}
	examined := 0
	for len(queue) > 0 && len(out.Pages) < in.MaxPages && examined < 30 {
		next := queue[0]
		queue = queue[1:]
		examined++
		nextURL, _ := parseURL(next)
		page, err := readPage(ctx, client, nextURL)
		if ctx.Err() != nil {
			return action.Result{}, ctx.Err()
		}
		if err != nil {
			if examined == 1 {
				return action.Result{}, err
			}
			out.Errors = append(out.Errors, fmt.Sprintf("Could not read %s", next))
			continue
		}
		if page.Status < 200 || page.Status >= 300 {
			if examined == 1 {
				return action.Result{}, fmt.Errorf("website returned HTTP %d", page.Status)
			}
			out.Errors = append(out.Errors, fmt.Sprintf("HTTP %d: %s", page.Status, next))
			continue
		}
		if !page.HTML {
			if examined == 1 {
				return action.Result{}, errors.New("website URL must return an HTML page")
			}
			out.SkippedNonHTMLPages++
			continue
		}
		if visited[page.URL.String()] {
			continue
		}
		visited[page.URL.String()] = true
		seen[page.URL.String()] = true
		out.Pages = append(out.Pages, Page{URL: page.URL.String(), Title: page.Title, StatusCode: page.Status})
		for _, rawLink := range page.Links {
			link, err := resolveLink(page.URL, rawLink)
			if err != nil {
				continue
			}
			if !sameHostname(link, target) {
				out.SkippedExternalLinks++
				continue
			}
			if seen[link.String()] {
				continue
			}
			seen[link.String()] = true
			if len(queue) >= 100 {
				out.Truncated = true
				continue
			}
			queue = append(queue, link.String())
		}
	}
	out.Truncated = out.Truncated || len(queue) > 0
	result, err := action.JSONResult(out)
	if err != nil {
		return action.Result{}, err
	}
	for _, p := range out.Pages {
		raw, _ := json.Marshal(PageInput{URL: p.URL})
		for _, name := range a.checks {
			result.NextActions = append(result.NextActions, action.NextAction{ActionName: name, Input: raw, Reason: "Review discovered page"})
		}
	}
	return result, nil
}
