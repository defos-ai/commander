package actions

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/defos-ai/commander/framework/agent/action"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
)

type Link struct {
	URL         string `json:"url"`
	StatusCode  int    `json:"status_code"`
	Error       string `json:"error,omitempty"`
	RedirectURL string `json:"redirect_url,omitempty"`
}
type CheckLinksOutput struct {
	URL                  string `json:"url"`
	Title                string `json:"title"`
	StatusCode           int    `json:"status_code"`
	CheckedLinks         int    `json:"checked_links"`
	BrokenLinks          int    `json:"broken_links"`
	SkippedExternalLinks int    `json:"skipped_external_links"`
	SkippedNonHTTPLinks  int    `json:"skipped_non_http_links"`
	Truncated            bool   `json:"truncated"`
	Links                []Link `json:"links"`
}
type CheckLinks struct{ *action.Base }

func NewCheckLinks() *CheckLinks {
	return &CheckLinks{action.NewBase(actionmodel.Action{Name: CheckLinksName, Description: "Check up to 40 unique links on the same hostname using HTTP GET, including HTTP(S) and port changes. Reports broken links and skipped external/non-HTTP links; does not follow forms or execute JavaScript.", InputSchema: json.RawMessage(pageInputSchema), OutputSchema: outputSchema[CheckLinksOutput]()})}
}
func (a *CheckLinks) Run(ctx context.Context, raw json.RawMessage) (action.Result, error) {
	if err := a.Check(); err != nil {
		return action.Result{}, err
	}
	var in PageInput
	if err := action.DecodeInput(raw, &in); err != nil {
		return action.Result{}, err
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
	page, err := readPage(ctx, client, target)
	if err != nil {
		return action.Result{}, err
	}
	out := CheckLinksOutput{URL: page.URL.String(), Title: page.Title, StatusCode: page.Status, Links: []Link{}}
	seen := map[string]bool{target.String(): true, page.URL.String(): true}
	for _, rawLink := range page.Links {
		link, err := resolveLink(page.URL, rawLink)
		if err != nil {
			out.SkippedNonHTTPLinks++
			continue
		}
		if seen[link.String()] {
			continue
		}
		seen[link.String()] = true
		if !sameHostname(link, target) {
			out.SkippedExternalLinks++
			continue
		}
		if out.CheckedLinks >= maxLinks {
			out.Truncated = true
			continue
		}
		result := Link{URL: link.String()}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, link.String(), nil)
		req.Header.Set("User-Agent", "Commander-Web-Agent/1.0")
		// Reuse httpClient's pinned IPs and redirect checks for same-hostname links.
		response, err := client.Do(req)
		if response != nil {
			response.Body.Close()
		}
		if ctx.Err() != nil {
			return action.Result{}, ctx.Err()
		}
		if err != nil {
			result.Error = "Request failed or redirect left website scope"
		} else {
			result.StatusCode = response.StatusCode
			if response.Request.URL.String() != link.String() {
				result.RedirectURL = response.Request.URL.String()
			}
		}
		out.CheckedLinks++
		if result.Error != "" || result.StatusCode >= 400 {
			out.BrokenLinks++
		}
		out.Links = append(out.Links, result)
	}
	return action.JSONResult(out)
}
