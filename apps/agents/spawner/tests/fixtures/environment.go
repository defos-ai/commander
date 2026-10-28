package fixtures

import (
	"testing"
)

func KubernetesEnvironment(t *testing.T) {
	t.Helper()
	for name, value := range map[string]string{
		"COMMANDER_AGENT_ID_SUFFIX": "", "SECRETS_ENCRYPTION_KEY": "", "SPAWNER_RUNTIME": "kubernetes",
		"SPAWNER_KUBERNETES_NAMESPACE": "agents", "SPAWNER_KUBERNETES_POD_PREFIX": "",
		"SPAWNER_KUBERNETES_SERVICE_ACCOUNT": "agent-worker", "SPAWNER_KUBERNETES_CONFIG_MAP": "agent-config",
		"SPAWNER_KUBERNETES_SECRET": "agent-credentials", "SPAWNER_KUBERNETES_IMAGE_PULL_SECRETS": "",
	} {
		t.Setenv(name, value)
	}
}
