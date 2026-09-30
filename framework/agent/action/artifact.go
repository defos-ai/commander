package action

import "commander/framework/artifact"

// Artifact groups files produced locally by an action. The agent uploads them
// and adds identity/ownership before announcing the artifact to the controller.
type Artifact struct {
	Name  string
	Files []artifact.FileSource
}
