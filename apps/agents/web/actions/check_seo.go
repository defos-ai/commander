package actions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/defos-ai/commander/framework/agent/action"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"

	"golang.org/x/net/html"
)

type SEOIssue struct {
	Check          string   `json:"check"`
	Severity       string   `json:"severity"`
	Evidence       []string `json:"evidence"`
	Recommendation string   `json:"recommendation"`
}

type SEOMetadata struct {
	Source  string `json:"source"`
	Content string `json:"content"`
}

type CheckSEOOutput struct {
	URL                  string        `json:"url"`
	StatusCode           int           `json:"status_code"`
	Title                string        `json:"title"`
	TitleCount           int           `json:"title_count"`
	MetaDescription      string        `json:"meta_description"`
	MetaDescriptionCount int           `json:"meta_description_count"`
	CanonicalURL         string        `json:"canonical_url"`
	CanonicalCount       int           `json:"canonical_count"`
	H1Count              int           `json:"h1_count"`
	H1s                  []string      `json:"h1s"`
	Viewport             string        `json:"viewport"`
	Robots               []SEOMetadata `json:"robots"`
	OpenGraph            []SEOMetadata `json:"open_graph"`
	StructuredDataBlocks int           `json:"structured_data_blocks"`
	InvalidJSONLDBlocks  int           `json:"invalid_json_ld_blocks"`
	Issues               []SEOIssue    `json:"issues"`
	Scope                string        `json:"scope"`
	Truncated            bool          `json:"truncated"`
}

type CheckSEO struct{ *action.Base }

func NewCheckSEO() *CheckSEO {
	return &CheckSEO{action.NewBase(actionmodel.Action{Name: CheckSEOName, Description: "Inspect static HTML and response headers for page title, description, canonical URL, headings, robots directives, viewport, Open Graph tags and JSON-LD syntax. Reports observed evidence and contextual recommendations; does not measure rankings or guarantee indexing.", InputSchema: json.RawMessage(pageInputSchema), OutputSchema: outputSchema[CheckSEOOutput]()})}
}

func (a *CheckSEO) Run(ctx context.Context, raw json.RawMessage) (action.Result, error) {
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
	client, err := httpClient(ctx, target)
	if err != nil {
		return action.Result{}, err
	}
	defer client.CloseIdleConnections()
	page, err := readPage(ctx, client, target)
	if err != nil {
		return action.Result{}, err
	}
	if page.Status < 200 || page.Status >= 300 {
		return action.Result{}, fmt.Errorf("website returned HTTP %d", page.Status)
	}
	if !page.HTML {
		return action.Result{}, errors.New("website URL must return an HTML page")
	}
	out := CheckSEOOutput{
		URL: page.URL.String(), StatusCode: page.Status,
		H1s: []string{}, Robots: []SEOMetadata{}, OpenGraph: []SEOMetadata{}, Issues: []SEOIssue{},
		Scope: "Static HTML and response headers from one page, up to 2 MiB; no JavaScript rendering, robots.txt, sitemap, external URL checks, ranking data or indexing verification. JSON-LD checks cover JSON syntax and object/array shape only, not schema validity or rich-result eligibility. Metadata and evidence samples are bounded.",
	}
	// Counts cover all elements in the bounded document; only reported samples are capped.
	clip := func(value string, limit int) string {
		value = strings.TrimSpace(value)
		if utf8.RuneCountInString(value) > limit {
			out.Truncated = true
			return boundedText(value, limit)
		}
		return value
	}
	addSample := func(samples *[]string, value string) {
		if len(*samples) >= 3 {
			out.Truncated = true
			return
		}
		*samples = append(*samples, clip(value, 300))
	}
	addMetadata := func(samples *[]SEOMetadata, source, content string) {
		if len(*samples) >= 10 {
			out.Truncated = true
			return
		}
		*samples = append(*samples, SEOMetadata{Source: clip(source, 80), Content: clip(content, 300)})
	}
	addIssue := func(check, severity, recommendation string, evidence ...string) {
		out.Issues = append(out.Issues, SEOIssue{Check: check, Severity: severity, Evidence: evidence, Recommendation: recommendation})
	}
	var titles, descriptions, canonicals, canonicalValues, restrictions, invalidJSONLD []string
	baseRaw := ""
	baseSeen, titlePresent, descriptionPresent, viewportPresent := false, false, false, false
	emptyHeadings := 0
	ogPresent := map[string]bool{}
	recordRobots := func(source, content string) {
		addMetadata(&out.Robots, source, content)
		for _, token := range strings.FieldsFunc(strings.ToLower(content), func(r rune) bool { return unicode.IsSpace(r) || r == ',' || r == ':' || r == ';' }) {
			if token == "noindex" || token == "none" {
				addSample(&restrictions, source+": "+content)
				break
			}
		}
	}
	for _, value := range page.Headers.Values("X-Robots-Tag") {
		recordRobots("X-Robots-Tag", value)
	}
	var visit func(*html.Node, bool)
	visit = func(node *html.Node, inHead bool) {
		if node.Type == html.ElementNode {
			// Inert templates and foreign content do not supply document SEO metadata.
			if node.Data == "template" || node.Namespace != "" {
				return
			}
			inHead = inHead || node.Data == "head"
			switch node.Data {
			case "title":
				if inHead {
					value := seoNodeText(node)
					out.TitleCount++
					titlePresent = titlePresent || value != ""
					if out.TitleCount == 1 {
						out.Title = clip(value, 300)
					}
					addSample(&titles, value)
				}
			case "base":
				if inHead && !baseSeen {
					for _, attr := range node.Attr {
						if attr.Key == "href" {
							baseRaw, baseSeen = strings.TrimSpace(attr.Val), true
							break
						}
					}
				}
			case "link":
				if inHead && seoHasToken(seoAttr(node, "rel"), "canonical") {
					value := seoAttr(node, "href")
					out.CanonicalCount++
					canonicalValues = append(canonicalValues, value)
					addSample(&canonicals, value)
				}
			case "meta":
				if !inHead {
					break
				}
				content := seoAttr(node, "content")
				switch strings.ToLower(seoAttr(node, "name")) {
				case "description":
					out.MetaDescriptionCount++
					descriptionPresent = descriptionPresent || content != ""
					if out.MetaDescriptionCount == 1 {
						out.MetaDescription = clip(content, 600)
					}
					addSample(&descriptions, content)
				case "viewport":
					if !viewportPresent {
						out.Viewport = clip(content, 300)
					}
					viewportPresent = viewportPresent || content != ""
				case "robots", "googlebot", "bingbot":
					recordRobots("meta[name="+strings.ToLower(seoAttr(node, "name"))+"]", content)
				}
				property := strings.ToLower(seoAttr(node, "property"))
				if strings.HasPrefix(property, "og:") {
					addMetadata(&out.OpenGraph, property, content)
					ogPresent[property] = ogPresent[property] || content != ""
				}
			case "h1":
				out.H1Count++
				value := seoNodeText(node)
				if value == "" {
					emptyHeadings++
				}
				addSample(&out.H1s, value)
			case "script":
				if strings.EqualFold(seoAttr(node, "type"), "application/ld+json") {
					out.StructuredDataBlocks++
					var contents strings.Builder
					for child := node.FirstChild; child != nil; child = child.NextSibling {
						if child.Type == html.TextNode {
							contents.WriteString(child.Data)
						}
					}
					value := strings.TrimSpace(contents.String())
					if !json.Valid([]byte(value)) || (len(value) > 0 && value[0] != '{' && value[0] != '[') {
						out.InvalidJSONLDBlocks++
						addSample(&invalidJSONLD, fmt.Sprintf("JSON-LD block %d is invalid JSON or is not an object/array", out.StructuredDataBlocks))
					}
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child, inHead)
		}
	}
	visit(page.Root, false)
	if err := ctx.Err(); err != nil {
		return action.Result{}, err
	}
	if !titlePresent {
		addIssue("missing-title", "warning", "Add a descriptive document title for this page.", "No non-empty title element in the document head")
	}
	if out.TitleCount > 1 {
		addIssue("duplicate-title", "warning", "Use one document title to avoid ambiguous metadata.", titles...)
	}
	if !descriptionPresent {
		addIssue("missing-meta-description", "warning", "Add a page-specific summary that search engines may use for a result snippet.", "No non-empty meta[name=description] in the document head")
	}
	if out.MetaDescriptionCount > 1 {
		addIssue("duplicate-meta-description", "warning", "Keep one clear meta description for this page.", descriptions...)
	}
	if out.CanonicalCount == 0 {
		addIssue("missing-canonical", "info", "Consider a canonical URL when equivalent pages can be reached through multiple URLs.", "No link[rel=canonical] in the document head")
	} else {
		if out.CanonicalCount > 1 {
			addIssue("duplicate-canonical", "warning", "Review the canonical declarations and retain one preferred URL.", canonicals...)
		}
		base := page.URL
		if baseSeen {
			if parsed, err := url.Parse(baseRaw); err == nil {
				base = page.URL.ResolveReference(parsed)
			}
		}
		var invalidCanonicals, externalCanonicals []string
		for i, value := range canonicalValues {
			canonical, err := resolveLink(base, value)
			if err != nil || value == "" {
				addSample(&invalidCanonicals, fmt.Sprintf("Canonical declaration %d: %s", i+1, value))
				continue
			}
			if i == 0 {
				out.CanonicalURL = clip(canonical.String(), 2048)
			}
			if !sameHostname(canonical, page.URL) {
				addSample(&externalCanonicals, canonical.String())
			}
		}
		if len(invalidCanonicals) > 0 {
			addIssue("invalid-canonical", "warning", "Use a non-empty HTTP(S) canonical URL without embedded credentials.", invalidCanonicals...)
		}
		if len(externalCanonicals) > 0 {
			addIssue("external-canonical", "info", "Confirm that choosing another hostname as the preferred page is intentional; the target was not fetched.", externalCanonicals...)
		}
	}
	if out.H1Count == 0 {
		addIssue("missing-h1", "info", "Consider a clear main heading that describes the page content.", "No h1 element in the static HTML")
	} else if emptyHeadings > 0 {
		addIssue("empty-h1", "warning", "Give main headings meaningful text or review whether client rendering supplies it.", fmt.Sprintf("%d of %d h1 elements contain no static text", emptyHeadings, out.H1Count))
	}
	if out.H1Count > 1 {
		addIssue("multiple-h1", "info", "Review the heading hierarchy for clarity; multiple h1 elements are not automatically an SEO error.", out.H1s...)
	}
	if !viewportPresent {
		addIssue("missing-viewport", "info", "Add viewport metadata and check that the page is usable at mobile widths.", "No non-empty meta[name=viewport] in the document head")
	}
	if len(restrictions) > 0 {
		addIssue("indexing-restricted", "info", "Confirm the indexing policy for the named crawlers. noindex/none can be intentional for previews or private content; a directive does not prove current index status.", restrictions...)
	}
	missingOG := []string{}
	for _, property := range []string{"og:title", "og:type", "og:image", "og:url"} {
		if !ogPresent[property] {
			missingOG = append(missingOG, property)
		}
	}
	if len(missingOG) > 0 {
		addIssue("incomplete-open-graph", "info", "Consider Open Graph metadata for predictable social sharing previews; this check does not predict search rankings.", "Missing or empty: "+strings.Join(missingOG, ", "))
	}
	if out.InvalidJSONLDBlocks > 0 {
		addIssue("invalid-json-ld", "warning", "Correct JSON-LD syntax and use an object or array; then validate against the intended structured-data vocabulary.", invalidJSONLD...)
	}
	return action.JSONResult(out)
}

func seoAttr(node *html.Node, name string) string {
	for _, attr := range node.Attr {
		if attr.Key == name {
			return strings.TrimSpace(attr.Val)
		}
	}
	return ""
}

func seoHasToken(value, token string) bool {
	for _, field := range strings.Fields(value) {
		if strings.EqualFold(field, token) {
			return true
		}
	}
	return false
}

func seoNodeText(node *html.Node) string {
	var text strings.Builder
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if node.Type == html.ElementNode && (node.Data == "script" || node.Data == "style" || node.Data == "template") {
			return
		}
		if node.Type == html.TextNode {
			text.WriteString(node.Data)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(node)
	return strings.Join(strings.Fields(text.String()), " ")
}

func boundedText(text string, limit int) string {
	text = strings.TrimSpace(text)
	runes := []rune(text)
	if len(runes) > limit {
		return string(runes[:limit]) + "…"
	}
	return text
}
