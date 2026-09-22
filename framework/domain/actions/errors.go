package actions

import "errors"

// ErrArtifactPersistence marks an action outcome whose evidence could not be saved.
var ErrArtifactPersistence = errors.New("persist action artifacts")

var ErrUnknownAction = errors.New("unknown action")
