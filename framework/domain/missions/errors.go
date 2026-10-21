package missions

import "errors"

var (
	ErrUnknownMission = errors.New("unknown mission")
	ErrMissionExists  = errors.New("mission already exists")
)
