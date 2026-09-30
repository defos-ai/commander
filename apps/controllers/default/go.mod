module commander/apps/controllers/default

go 1.26.7

require (
	commander/framework v0.0.0
	commander/providers/fs v0.0.0
)

require (
	github.com/google/jsonschema-go v0.4.3 // indirect
	golang.org/x/sys v0.48.0 // indirect
)

replace commander/framework => ../../../framework

replace commander/providers/fs => ../../../providers/fs
