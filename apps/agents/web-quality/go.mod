module commander/apps/agents/web-quality

go 1.26.7

require (
	commander/framework v0.0.0
	commander/providers v0.0.0
	github.com/chromedp/cdproto v0.0.0-20260714215040-dc233986426f
	github.com/chromedp/chromedp v0.16.0
	golang.org/x/net v0.59.0
)

require (
	github.com/chromedp/sysutil v1.1.0 // indirect
	github.com/go-json-experiment/json v0.0.0-20260623181947-01eb4420fa68 // indirect
	github.com/gobwas/httphead v0.1.0 // indirect
	github.com/gobwas/pool v0.2.1 // indirect
	github.com/gobwas/ws v1.4.0 // indirect
	github.com/google/jsonschema-go v0.4.3 // indirect
	golang.org/x/sys v0.48.0 // indirect
)

replace commander/framework => ../../../framework

replace commander/providers => ../../../providers
