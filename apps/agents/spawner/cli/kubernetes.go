package cli

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/defos-ai/commander/apps/agents/spawner/runtimes/kubernetes"
	s3store "github.com/defos-ai/commander/providers/s3/store"
)

func kubernetesConfiguration() (kubernetes.Config, error) {
	result := kubernetes.Config{
		PodPrefix: os.Getenv("SPAWNER_KUBERNETES_POD_PREFIX"), RunAsUser: 1000, RunAsGroup: 1000,
		Resources: kubernetes.ResourceRequirements{
			Requests: map[string]string{"cpu": "100m", "memory": "128Mi"},
			Limits:   map[string]string{"cpu": "500m", "memory": "512Mi"},
		},
	}
	for _, setting := range []struct {
		name string
		out  *string
	}{
		{"NAMESPACE", &result.Namespace}, {"SERVICE_ACCOUNT", &result.ServiceAccount},
		{"CONFIG_MAP", &result.ConfigMap}, {"SECRET", &result.Secret},
	} {
		name := "SPAWNER_KUBERNETES_" + setting.name
		value := os.Getenv(name)
		if strings.TrimSpace(value) == "" {
			return kubernetes.Config{}, fmt.Errorf("%s is required for the kubernetes runtime", name)
		}
		*setting.out = value
	}
	refs, err := parseList(os.Getenv("SPAWNER_KUBERNETES_IMAGE_PULL_SECRETS"))
	if err != nil {
		return kubernetes.Config{}, errors.New("SPAWNER_KUBERNETES_IMAGE_PULL_SECRETS must contain unique nonempty comma-separated references")
	}
	result.ImagePullSecrets = refs
	if err := result.Validate(); err != nil {
		return kubernetes.Config{}, fmt.Errorf("SPAWNER_KUBERNETES configuration: %w", err)
	}
	return result, nil
}

func validateKubernetesProviders(busURL, storageURL string) error {
	bus, err := url.Parse(busURL)
	if err != nil || (bus.Scheme != "amqp" && bus.Scheme != "amqps") || bus.Hostname() == "" || bus.Opaque != "" {
		return errors.New("kubernetes runtime requires an amqp or amqps bus-url with a host reachable from spawned Pods")
	}
	if connectionScheme(storageURL) != "s3" {
		return errors.New("kubernetes runtime requires an s3 storage-url reachable from spawned Pods")
	}
	if _, _, err := s3store.ParseURL(storageURL); err != nil {
		return fmt.Errorf("kubernetes storage-url: %w", err)
	}
	return nil
}
