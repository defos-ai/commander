package data

import _ "embed"

// DefaultMissions is bundled for opt-in launchers such as Playground.
// The standalone controller still loads missions only with --with-missions.
//
//go:embed default-missions.json
var DefaultMissions []byte
