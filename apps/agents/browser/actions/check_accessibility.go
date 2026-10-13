package actions

import (
	"context"
	"encoding/json"
	"sync/atomic"

	"github.com/defos-ai/commander/framework/agent/action"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"

	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

type AccessibilityIssue struct {
	Check    string   `json:"check"`
	Count    int      `json:"count"`
	Examples []string `json:"examples"`
}
type CheckAccessibilityOutput struct {
	URL           string               `json:"url"`
	Title         string               `json:"title"`
	Language      string               `json:"language"`
	Scope         string               `json:"scope"`
	Issues        []AccessibilityIssue `json:"issues"`
	BrowserErrors []string             `json:"browser_errors"`
	Truncated     bool                 `json:"truncated"`
}
type CheckAccessibility struct{ *browserAction }

func NewCheckAccessibility() *CheckAccessibility {
	return &CheckAccessibility{&browserAction{Base: action.NewBase(actionmodel.Action{Name: CheckAccessibilityName, Description: "Render a page in Chromium and check basic title, language, image alternatives, control labels and accessible names. Captures JavaScript errors. This is a limited diagnostic, not a full WCAG audit.", InputSchema: json.RawMessage(pageInputSchema), OutputSchema: outputSchema[CheckAccessibilityOutput]()})}}
}
func (a *CheckAccessibility) Run(ctx context.Context, raw json.RawMessage) (action.Result, error) {
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
	browserCtx, cancel, err := a.browser(ctx, target)
	if err != nil {
		return action.Result{}, err
	}
	defer cancel()
	errors := make(chan string, 10)
	var dropped atomic.Bool
	chromedp.ListenTarget(browserCtx, func(event any) {
		if e, ok := event.(*runtime.EventExceptionThrown); ok {
			message := e.ExceptionDetails.Text
			if e.ExceptionDetails.Exception != nil && e.ExceptionDetails.Exception.Description != "" {
				message = e.ExceptionDetails.Exception.Description
			}
			select {
			case errors <- boundedText(message, 300):
			default:
				dropped.Store(true)
			}
		}
	})
	out := CheckAccessibilityOutput{Scope: "Basic rendered-page checks only; excludes contrast, keyboard navigation, assistive technology and full WCAG conformance."}
	if err := chromedp.Run(browserCtx, chromedp.EmulateViewport(1280, 800), chromedp.Navigate(target.String()), chromedp.Evaluate(accessibilityScript, &out)); err != nil {
		return action.Result{}, err
	}
	out.Scope = "Basic rendered-page checks only; excludes contrast, keyboard navigation, assistive technology and full WCAG conformance."
	out.BrowserErrors = make([]string, 0, len(errors))
	for range len(errors) {
		out.BrowserErrors = append(out.BrowserErrors, <-errors)
	}
	out.Truncated = out.Truncated || dropped.Load()
	return action.JSONResult(out)
}

// Fixed application-owned script: callers cannot submit JavaScript for execution.
const accessibilityScript = `(() => {
 const issues=[]; let truncated=false;
 const text=(value)=>(value||'').trim();
 const example=(el)=>el.outerHTML.slice(0,180);
 const add=(check,elements)=>{if(elements.length)issues.push({check,count:elements.length,examples:elements.slice(0,3).map(example)})};
 const sample=(selector)=>{const all=document.querySelectorAll(selector);if(all.length>5000)truncated=true;return Array.from(all).slice(0,5000)};
 const labelled=(el)=>text(el.getAttribute('aria-label')) || text(el.getAttribute('aria-labelledby')).split(/\s+/).some(id=>id&&text(document.getElementById(id)?.textContent));
 const named=(el)=>labelled(el)||text(el.textContent)||text(el.getAttribute('title'))||Array.from(el.querySelectorAll('img[alt]')).some(img=>text(img.alt));
 if(!text(document.title))issues.push({check:'missing-title',count:1,examples:['document.title is empty']});
 if(!text(document.documentElement.lang))issues.push({check:'missing-language',count:1,examples:['html lang is empty']});
 add('image-missing-alt',sample('img').filter(el=>!el.hasAttribute('alt')));
 add('control-missing-label',sample('input:not([type="hidden"]):not([type="submit"]):not([type="button"]):not([type="reset"]):not([type="image"]),select,textarea').filter(el=>!labelled(el)&&!text(el.getAttribute('title'))&&!Array.from(el.labels||[]).some(label=>text(label.textContent))));
 add('button-missing-name',sample('button,[role="button"]').filter(el=>!named(el)));
 add('link-missing-name',sample('a[href]').filter(el=>!named(el)));
 return {url:location.href,title:document.title.slice(0,300),language:document.documentElement.lang.slice(0,40),issues,truncated};
})()`
