package actions

import (
	"context"
	"encoding/json"

	"commander/framework/agent/action"
	"commander/framework/artifact"
	actionmodel "commander/framework/domain/actions"

	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

type Viewport struct {
	Name               string `json:"name"`
	Width              int    `json:"width"`
	Height             int    `json:"height"`
	HorizontalOverflow bool   `json:"horizontal_overflow"`
	ArtifactName       string `json:"artifact_name"`
}
type CapturePageOutput struct {
	URL       string     `json:"url"`
	Title     string     `json:"title"`
	Viewports []Viewport `json:"viewports"`
}
type CapturePage struct{ *browserAction }

func NewCapturePage() *CapturePage {
	return &CapturePage{&browserAction{Base: action.NewBase(actionmodel.Action{Name: CapturePageName, Description: "Capture desktop and mobile viewport PNG screenshots with Chromium and report horizontal overflow. Returns two image artifacts for visual review.", RequiredPermissions: []string{"network-access", "execute-command"}, InputSchema: json.RawMessage(pageInputSchema), OutputSchema: outputSchema[CapturePageOutput]()})}}
}
func (a *CapturePage) Run(ctx context.Context, raw json.RawMessage, grants []string) (action.Result, error) {
	if err := a.Check(grants); err != nil {
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
	out := CapturePageOutput{URL: target.String(), Viewports: []Viewport{{Name: "desktop", Width: 1280, Height: 800, ArtifactName: "desktop.png"}, {Name: "mobile", Width: 390, Height: 844, ArtifactName: "mobile.png"}}}
	artifacts := []artifact.Artifact{}
	for i := range out.Viewports {
		v := &out.Viewports[i]
		var screenshot []byte
		if err := chromedp.Run(browserCtx, chromedp.EmulateViewport(int64(v.Width), int64(v.Height)), chromedp.Navigate(target.String()), chromedp.Evaluate(`document.fonts.ready.then(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))))`, nil, func(p *runtime.EvaluateParams) *runtime.EvaluateParams { return p.WithAwaitPromise(true) }), chromedp.Title(&out.Title), chromedp.Location(&out.URL), chromedp.Evaluate(`document.documentElement.scrollWidth > innerWidth + 1`, &v.HorizontalOverflow), chromedp.CaptureScreenshot(&screenshot)); err != nil {
			return action.Result{}, err
		}
		artifacts = append(artifacts, artifact.Artifact{Name: v.ArtifactName, MediaType: "image/png", Data: screenshot})
	}
	out.Title = boundedText(out.Title, 300)
	result, err := action.JSONResult(out)
	result.Artifacts = artifacts
	return result, err
}
