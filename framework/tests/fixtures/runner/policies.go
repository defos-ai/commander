package fixtures

import "github.com/defos-ai/commander/framework/domain/missions/policies"

func SharingPolicy() policies.SharingPolicy {
	return policies.SharingPolicy{{
		FromActions: []string{"discover"},
		ToActions:   []string{"inspect"},
		Include:     []policies.SharingKind{policies.ShareResults, policies.ShareArtifacts},
	}}
}
