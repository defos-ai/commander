module commander/adapters/mcp

go 1.26.7

require (
	commander/framework v0.0.0
	commander/providers/fs v0.0.0
	github.com/modelcontextprotocol/go-sdk v1.7.0
)

require (
	github.com/google/jsonschema-go v0.4.3 // indirect
	github.com/segmentio/asm v1.1.3 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	golang.org/x/oauth2 v0.35.0 // indirect
	golang.org/x/sync v0.23.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/time v0.15.0 // indirect
	golang.org/x/tools v0.49.0 // indirect
)

replace commander/framework => ../../../framework

replace commander/providers/fs => ../../../providers/fs
