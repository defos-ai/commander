package fixtures

import (
	"strings"

	"github.com/defos-ai/commander/apps/agents/spawner/runtimes/kubernetes"
)

func PodConfig() kubernetes.Config {
	return kubernetes.Config{
		Namespace: "agents", ServiceAccount: "agent-worker", ConfigMap: "provider-config", Secret: "provider-secrets",
		ImagePullSecrets: []string{"registry-pull"}, RunAsUser: 1000, RunAsGroup: 1001,
		Resources: kubernetes.ResourceRequirements{
			Requests: map[string]string{"cpu": "250m", "memory": "256Mi"},
			Limits:   map[string]string{"cpu": "1", "memory": "1Gi"},
		},
	}
}

func PodSpec() kubernetes.Spec {
	return kubernetes.Spec{
		RequestID: "command:11111111-1111-4111-8111-111111111111", Owner: "22222222-2222-4222-8222-222222222222",
		AgentID: "example-agent-12345678", AgentType: "example-agent", Package: ActionPackage,
		Image: "ghcr.io/defos-ai/example-agent@sha256:" + strings.Repeat("a", 64),
	}
}
