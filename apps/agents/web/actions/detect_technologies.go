package actions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/defos-ai/commander/framework/agent/action"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"

	"golang.org/x/net/html"
)

const (
	maxTechnologyIndicators = 48
	maxTechnologyScripts    = 200
	maxTechnologyNodes      = 20000
)

type TechnologyHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// TechnologyIndicator describes a declaration in the response, not verified runtime use.
type TechnologyIndicator struct {
	Name     string `json:"name"`
	Category string `json:"category"`
	Version  string `json:"version,omitempty"`
	Source   string `json:"source"`
	Evidence string `json:"evidence"`
}

type DetectTechnologiesOutput struct {
	URL              string                `json:"url"`
	StatusCode       int                   `json:"status_code"`
	IsHTML           bool                  `json:"is_html"`
	ServerHeaders    []TechnologyHeader    `json:"server_headers"`
	Indicators       []TechnologyIndicator `json:"indicators"`
	ScriptsInspected int                   `json:"scripts_inspected"`
	Truncated        bool                  `json:"truncated"`
	Scope            string                `json:"scope"`
	Limitations      []string              `json:"limitations"`
}

type DetectTechnologies struct{ *action.Base }

func NewDetectTechnologies() *DetectTechnologies {
	return &DetectTechnologies{action.NewBase(actionmodel.Action{
		Name:        DetectTechnologiesName,
		Description: "Inspect one HTTP response for Server/X-Powered-By headers, generator metadata, and recognizable JavaScript package/framework declarations. Reports explicit versions and evidence; does not fetch scripts, execute JavaScript, or assess vulnerabilities or current versions.",
		InputSchema: json.RawMessage(pageInputSchema), OutputSchema: outputSchema[DetectTechnologiesOutput](),
	})}
}

func (a *DetectTechnologies) Run(ctx context.Context, raw json.RawMessage) (action.Result, error) {
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
		// http.Client errors include the requested URL, which may contain query secrets.
		var requestError *url.Error
		if errors.As(err, &requestError) {
			err = requestError.Err
		}
		return action.Result{}, fmt.Errorf("inspect page technologies: %w", err)
	}
	out := DetectTechnologiesOutput{
		StatusCode: page.Status, IsHTML: page.HTML,
		ServerHeaders: []TechnologyHeader{}, Indicators: []TechnologyIndicator{},
		Scope: "Passive inspection of one final HTTP response and its static HTML; all findings are declared indicators, not proof of runtime use.",
		Limitations: []string{
			"Headers and metadata can be hidden, changed, or supplied by a proxy; they do not establish the origin server's actual software.",
			"Only recognizable script filenames, explicit package version paths, and selected HTML markers are inspected. Bundled, renamed, dynamically loaded, and unrecognized packages may be missed.",
			"Script bodies are neither fetched nor executed. Referenced packages may fail to load or never run; versions are reported only when explicitly declared.",
			"No vulnerability database or current release lookup is performed. These findings do not establish whether software is outdated, vulnerable, or secure.",
			"Output excludes URL credentials, queries, and fragments. Inspection is limited to 200 scripts, 20000 HTML nodes, 8 selected header values, and 48 indicators; evidence is limited to 240 characters.",
		},
	}
	out.URL = out.limitText(technologyURL(page.URL), 2048)
	seen := map[TechnologyIndicator]bool{}
	add := func(indicator TechnologyIndicator) {
		indicator.Name = out.limitText(indicator.Name, 80)
		if len(indicator.Version) > 40 {
			// A shortened version could describe a different release.
			indicator.Version = ""
			out.Truncated = true
		}
		indicator.Evidence = out.limitText(indicator.Evidence, 240)
		if seen[indicator] {
			return
		}
		seen[indicator] = true
		if len(out.Indicators) >= maxTechnologyIndicators {
			out.Truncated = true
			return
		}
		out.Indicators = append(out.Indicators, indicator)
	}
	for _, name := range []string{"Server", "X-Powered-By"} {
		for _, value := range page.Headers.Values(name) {
			if len(out.ServerHeaders) >= 8 {
				out.Truncated = true
				break
			}
			value = out.limitText(redactTechnologyURLs(strings.TrimSpace(value)), 240)
			out.ServerHeaders = append(out.ServerHeaders, TechnologyHeader{Name: name, Value: value})
			if match := technologyHeaderProduct.FindStringSubmatch(value); match != nil {
				add(TechnologyIndicator{Name: match[1], Category: "server", Version: match[2], Source: "response-header:" + name, Evidence: value})
			}
		}
	}
	if !page.HTML || page.Root == nil {
		return action.JSONResult(out)
	}
	// Walk depth-first without recursive calls or an unbounded traversal stack.
	nodes := 0
	for node := page.Root; node != nil; {
		if err := ctx.Err(); err != nil {
			return action.Result{}, err
		}
		if nodes >= maxTechnologyNodes {
			out.Truncated = true
			break
		}
		nodes++
		inert := node.Namespace != "" || (node.Type == html.ElementNode && node.Data == "template")
		if node.Type == html.ElementNode && !inert {
			switch node.Data {
			case "meta":
				if strings.EqualFold(technologyAttribute(node, "name"), "generator") {
					value := out.limitText(redactTechnologyURLs(strings.TrimSpace(technologyAttribute(node, "content"))), 240)
					if value != "" {
						name, version := technologyGenerator(value)
						add(TechnologyIndicator{Name: name, Category: "generator", Version: version, Source: "meta-generator", Evidence: value})
					}
				}
			case "script":
				scriptType := strings.ToLower(strings.TrimSpace(technologyAttribute(node, "type")))
				id := technologyAttribute(node, "id")
				if scriptType == "application/json" && (id == "__NEXT_DATA__" || id == "__NUXT_DATA__") {
					name := "Next.js"
					if id == "__NUXT_DATA__" {
						name = "Nuxt"
					}
					add(TechnologyIndicator{Name: name, Category: "framework", Source: "html-marker", Evidence: "script type=application/json id=" + id})
				}
				src := strings.TrimSpace(technologyAttribute(node, "src"))
				if src != "" && (scriptType == "" || scriptType == "module" || scriptType == "text/javascript" || scriptType == "application/javascript") {
					if out.ScriptsInspected >= maxTechnologyScripts || len(src) > 4096 {
						out.Truncated = true
					} else {
						out.ScriptsInspected++
						inspectTechnologyScript(src, add)
					}
				}
			}
			for _, attr := range node.Attr {
				switch attr.Key {
				case "ng-version":
					if explicitTechnologyVersion.MatchString(attr.Val) {
						add(TechnologyIndicator{Name: "Angular", Category: "framework", Version: attr.Val, Source: "html-marker", Evidence: "ng-version=" + attr.Val})
					}
				case "data-reactroot":
					add(TechnologyIndicator{Name: "React", Category: "framework", Source: "html-marker", Evidence: "data-reactroot attribute"})
				case "data-v-app":
					add(TechnologyIndicator{Name: "Vue", Category: "framework", Source: "html-marker", Evidence: "data-v-app attribute"})
				}
			}
		}
		if node.FirstChild != nil && !inert {
			node = node.FirstChild
			continue
		}
		for node != nil && node.NextSibling == nil {
			node = node.Parent
		}
		if node != nil {
			node = node.NextSibling
		}
	}
	return action.JSONResult(out)
}

func (out *DetectTechnologiesOutput) limitText(value string, limit int) string {
	runes := []rune(value)
	if len(runes) > limit {
		out.Truncated = true
		return string(runes[:limit])
	}
	return value
}

func technologyAttribute(node *html.Node, name string) string {
	for _, attr := range node.Attr {
		if attr.Key == name {
			return attr.Val
		}
	}
	return ""
}

func technologyURL(value *url.URL) string {
	clean := *value
	clean.User, clean.RawQuery, clean.Fragment, clean.RawFragment, clean.ForceQuery = nil, "", "", "", false
	return clean.String()
}

var technologyURLPattern = regexp.MustCompile(`(?i)(?:[a-z][a-z0-9+.-]*:)?//[^\s<>"']+`)

func redactTechnologyURLs(value string) string {
	return technologyURLPattern.ReplaceAllStringFunc(value, func(raw string) string {
		parsed, err := url.Parse(raw)
		if err != nil {
			return "[redacted URL]"
		}
		return technologyURL(parsed)
	})
}

const technologyVersionPattern = `[0-9]+(?:\.[0-9]+){0,3}(?:[-+][A-Za-z0-9.-]+?)?`

var explicitTechnologyVersion = regexp.MustCompile(`^` + technologyVersionPattern + `$`)
var technologyHeaderProduct = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9._+-]*)(?:/(` + technologyVersionPattern + `))?(?:$|[ ;,(])`)
var technologyGeneratorPattern = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9._!+-]*)(?:[ /]v?(` + technologyVersionPattern + `))?(?:$|[ (;])`)

func technologyGenerator(value string) (string, string) {
	if match := technologyGeneratorPattern.FindStringSubmatch(value); match != nil {
		return match[1], match[2]
	}
	return "Unrecognized generator", ""
}

type scriptTechnology struct {
	name    string
	file    *regexp.Regexp
	version *regexp.Regexp
}

func newScriptTechnology(name, packageName, basename, suffix string) scriptTechnology {
	return scriptTechnology{
		name:    name,
		file:    regexp.MustCompile(`(?i)^` + basename + `(?:[-.]v?(` + technologyVersionPattern + `))?` + suffix + `\.js$`),
		version: regexp.MustCompile(`(?i)(?:^|/)` + regexp.QuoteMeta(packageName) + `(?:@|/)v?(` + technologyVersionPattern + `)(?:/|$)`),
	}
}

var scriptTechnologies = []scriptTechnology{
	newScriptTechnology("jQuery", "jquery", "jquery", `(?:\.slim)?(?:\.min)?`),
	newScriptTechnology("jQuery UI", "jquery-ui", "jquery-ui", `(?:\.min)?`),
	newScriptTechnology("Bootstrap", "bootstrap", "bootstrap", `(?:\.bundle)?(?:\.min)?`),
	newScriptTechnology("React", "react", "react", `(?:\.production|\.development)?(?:\.min)?`),
	newScriptTechnology("React DOM", "react-dom", "react-dom", `(?:\.production|\.development)?(?:\.min)?`),
	newScriptTechnology("Vue", "vue", "vue", `(?:\.runtime)?(?:\.global|\.esm-browser|\.esm-bundler|\.common)?(?:\.prod|\.min)?`),
	newScriptTechnology("AngularJS", "angular", "angular", `(?:\.min)?`),
	newScriptTechnology("Lodash", "lodash", "lodash", `(?:\.min)?`),
	newScriptTechnology("Underscore", "underscore", "underscore", `(?:\.min)?`),
	newScriptTechnology("Moment.js", "moment", "moment", `(?:-with-locales)?(?:\.min)?`),
	newScriptTechnology("D3", "d3", "d3", `(?:\.min)?`),
	newScriptTechnology("HTMX", "htmx.org", "htmx", `(?:\.min)?`),
}

func inspectTechnologyScript(raw string, add func(TechnologyIndicator)) {
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "" && parsed.Scheme != "http" && parsed.Scheme != "https") {
		return
	}
	evidence := "script src=" + technologyURL(parsed)
	basename := path.Base(parsed.Path)
	for _, technology := range scriptTechnologies {
		match := technology.file.FindStringSubmatch(basename)
		if match == nil {
			continue
		}
		version := match[1]
		if packageMatch := technology.version.FindStringSubmatch(parsed.Path); packageMatch != nil {
			if version == "" {
				version = packageMatch[1]
			} else if version != packageMatch[1] {
				// Conflicting explicit declarations must remain visible as separate evidence.
				add(TechnologyIndicator{Name: technology.name, Category: "javascript-package", Version: packageMatch[1], Source: "script-package-path", Evidence: evidence})
			}
		}
		add(TechnologyIndicator{Name: technology.name, Category: "javascript-package", Version: version, Source: "script-src", Evidence: evidence})
	}
	if strings.HasPrefix(parsed.Path, "/_next/static/") {
		add(TechnologyIndicator{Name: "Next.js", Category: "framework", Source: "script-src", Evidence: evidence})
	}
	if strings.HasPrefix(parsed.Path, "/_nuxt/") {
		add(TechnologyIndicator{Name: "Nuxt", Category: "framework", Source: "script-src", Evidence: evidence})
	}
}
