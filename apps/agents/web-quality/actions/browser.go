package actions

import (
	"context"
	"errors"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"commander/framework/agent/action"
	"commander/framework/agent/environment"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

type browserAction struct {
	*action.Base
	mu         sync.RWMutex
	executable string
	noSandbox  bool
}

func (a *browserAction) Discover(ctx context.Context, env environment.Environment, scope *environment.DiscoveryScope) (action.Status, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.executable = ""
	a.SetStatus(action.Unknown)
	if env == nil {
		env = &environment.Local{}
	}
	configured, _ := env.LookupEnv("CHROMIUM_PATH")
	names := []string{"chromium", "chromium-browser", "google-chrome", "google-chrome-stable"}
	if configured != "" {
		if !filepath.IsAbs(configured) {
			return action.Unknown, errors.New("CHROMIUM_PATH must be an absolute executable path")
		}
		// An explicit discovery scope still wins over environment configuration.
		if scope == nil || scope.Paths == nil {
			// Packaged Chrome may link /usr/bin/google-chrome into /opt.
			// Use its real location so our inferred scope includes the target.
			if resolved, err := filepath.EvalSymlinks(configured); err == nil {
				configured = resolved
			}
			scope = &environment.DiscoveryScope{Paths: []string{filepath.Dir(configured)}}
		}
		names = []string{filepath.Base(configured)}
	}
	for _, name := range names {
		path, err := env.FindExecutable(ctx, name, scope)
		if err != nil {
			return action.Unknown, err
		}
		if path != "" {
			a.executable = path
			break
		}
	}
	noSandbox, _ := env.LookupEnv("CHROMIUM_NO_SANDBOX")
	a.noSandbox = false
	if noSandbox != "" {
		value, err := strconv.ParseBool(noSandbox)
		if err != nil {
			return action.Unknown, errors.New("CHROMIUM_NO_SANDBOX must be a boolean")
		}
		a.noSandbox = value
	}
	if a.executable == "" {
		a.SetUnavailable("Install Chromium or set CHROMIUM_PATH")
		return action.Unavailable, nil
	}
	a.SetStatus(action.Ready)
	return action.Ready, nil
}

// Each execution gets a fresh browser profile. Only GET/HEAD requests are allowed;
// document navigation stays on the requested hostname. External HTTP(S) resources
// remain available so fonts, styles and images render as they do for a visitor.
func (a *browserAction) browser(ctx context.Context, target *url.URL) (context.Context, context.CancelFunc, error) {
	a.mu.RLock()
	path, noSandbox := a.executable, a.noSandbox
	a.mu.RUnlock()
	if path == "" {
		return nil, nil, errors.New("browser is unavailable; run discovery first")
	}
	timeoutCtx, timeoutCancel := context.WithTimeout(ctx, 45*time.Second)
	options := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
	options = append(options, chromedp.ExecPath(path), chromedp.Flag("no-sandbox", noSandbox), chromedp.Flag("disable-dev-shm-usage", true), chromedp.Flag("disable-extensions", true))
	allocCtx, allocCancel := chromedp.NewExecAllocator(timeoutCtx, options...)
	browserCtx, browserCancel := chromedp.NewContext(allocCtx)
	cancel := func() { browserCancel(); allocCancel(); timeoutCancel() }
	chromedp.ListenTarget(browserCtx, func(event any) {
		paused, ok := event.(*fetch.EventRequestPaused)
		if !ok {
			return
		}
		// CDP listeners cannot synchronously issue commands to their own target.
		go func() {
			executorCtx := cdp.WithExecutor(browserCtx, chromedp.FromContext(browserCtx).Target)
			u, err := url.Parse(paused.Request.URL)
			allowed := err == nil && (u.Scheme == "http" || u.Scheme == "https" || u.Scheme == "data") && u.User == nil
			allowed = allowed && (paused.Request.Method == "GET" || paused.Request.Method == "HEAD")
			if paused.ResourceType == network.ResourceTypeDocument {
				allowed = allowed && sameHostname(u, target)
			}
			if !allowed {
				_ = fetch.FailRequest(paused.RequestID, network.ErrorReasonBlockedByClient).Do(executorCtx)
				return
			}
			_ = fetch.ContinueRequest(paused.RequestID).Do(executorCtx)
		}()
	})
	if err := chromedp.Run(browserCtx, fetch.Enable()); err != nil {
		cancel()
		return nil, nil, err
	}
	return browserCtx, cancel, nil
}

func boundedText(text string, limit int) string {
	text = strings.TrimSpace(text)
	runes := []rune(text)
	if len(runes) > limit {
		return string(runes[:limit]) + "…"
	}
	return text
}
