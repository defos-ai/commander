module commander/providers/fs

go 1.26.7

require (
	commander/framework v0.0.0
	golang.org/x/sys v0.48.0
)

require github.com/google/jsonschema-go v0.4.3 // indirect

replace commander/framework => ../../framework
