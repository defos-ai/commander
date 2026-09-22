package actions

import "errors"

var ErrArtifactPersistence = errors.New("persist action artifacts")

var ErrUnknownAction = errors.New("unknown action")
