package kubernetes

import (
	"crypto/sha256"
	"encoding/base32"
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"github.com/defos-ai/commander/framework/agent/manifest"
	"github.com/defos-ai/commander/framework/agent/packages"
	"github.com/defos-ai/commander/framework/id"
)

const SchedulingGate = "ai.defos.commander/launch"

type Metadata struct {
	Name              string            `json:"name,omitempty"`
	Namespace         string            `json:"namespace,omitempty"`
	UID               string            `json:"uid,omitempty"`
	ResourceVersion   string            `json:"resourceVersion,omitempty"`
	Labels            map[string]string `json:"labels,omitempty"`
	Annotations       map[string]string `json:"annotations,omitempty"`
	OwnerReferences   []json.RawMessage `json:"ownerReferences,omitempty"`
	Finalizers        []string          `json:"finalizers,omitempty"`
	DeletionTimestamp *string           `json:"deletionTimestamp,omitempty"`
}

type Pod struct {
	APIVersion string          `json:"apiVersion"`
	Kind       string          `json:"kind"`
	Metadata   Metadata        `json:"metadata"`
	Spec       json.RawMessage `json:"spec"`
	Status     json.RawMessage `json:"status,omitempty"`
}

type ResourceRequirements struct {
	Requests map[string]string `json:"requests"`
	Limits   map[string]string `json:"limits"`
}

// Config contains deployment choices, never model-provided action inputs.
type Config struct {
	Namespace        string
	PodPrefix        string
	ServiceAccount   string
	ConfigMap        string
	Secret           string
	ImagePullSecrets []string
	Resources        ResourceRequirements
	RunAsUser        int64
	RunAsGroup       int64
}

type Spec struct {
	RequestID string
	Owner     string
	AgentID   string
	AgentType string
	Package   string
	Image     string
}

var (
	podDNSLabelPattern     = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
	podDNSSubdomainPattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`)
)

func (c Config) Validate() error {
	if len(c.Namespace) > 63 || !podDNSLabelPattern.MatchString(c.Namespace) {
		return errors.New("Kubernetes namespace must be a DNS label of at most 63 characters")
	}
	if !validPodDNSName(c.PodPrefix + "a") {
		return errors.New("Kubernetes Pod prefix must form a DNS subdomain when followed by an agent ID")
	}
	for _, name := range []string{c.ServiceAccount, c.ConfigMap, c.Secret} {
		if !validPodDNSName(name) {
			return errors.New("Kubernetes ServiceAccount, ConfigMap and Secret names are required DNS subdomains")
		}
	}
	for _, name := range c.ImagePullSecrets {
		if !validPodDNSName(name) {
			return errors.New("Kubernetes image-pull Secret names must be DNS subdomains")
		}
	}
	for _, values := range []map[string]string{c.Resources.Requests, c.Resources.Limits} {
		if strings.TrimSpace(values["cpu"]) == "" || strings.TrimSpace(values["memory"]) == "" {
			return errors.New("Kubernetes CPU and memory requests and limits are required")
		}
	}
	if c.RunAsUser <= 0 || c.RunAsGroup <= 0 {
		return errors.New("Kubernetes run-as user and group must be positive")
	}
	return nil
}

// BuildPod prepares one gated worker. It reads no credentials or process
// environment; Kubernetes resolves only the explicitly selected reference keys.
func BuildPod(config Config, spec Spec) (Pod, error) {
	if err := config.Validate(); err != nil {
		return Pod{}, err
	}
	if err := validatePodLaunch(spec); err != nil {
		return Pod{}, err
	}
	name := config.PodPrefix + spec.AgentID
	if !validPodDNSName(name) {
		return Pod{}, errors.New("Kubernetes Pod name must be a DNS subdomain of at most 253 characters")
	}
	// Kubernetes validates resource quantities. Do not duplicate its quantity
	// grammar here; marshaling also detaches the returned body from config maps.
	body := map[string]any{
		"serviceAccountName":           config.ServiceAccount,
		"automountServiceAccountToken": false,
		"enableServiceLinks":           false,
		"restartPolicy":                "Never",
		"os":                           map[string]string{"name": "linux"},
		"nodeSelector":                 map[string]string{"kubernetes.io/os": "linux"},
		"schedulingGates":              []map[string]string{{"name": SchedulingGate}},
		"securityContext": map[string]any{
			"runAsNonRoot": true, "runAsUser": config.RunAsUser, "runAsGroup": config.RunAsGroup,
			"seccompProfile": map[string]string{"type": "RuntimeDefault"},
		},
		"containers": []map[string]any{{
			"name": "agent", "image": spec.Image, "imagePullPolicy": "IfNotPresent",
			"env":       podEnvironment(config, spec.AgentID),
			"resources": config.Resources,
			"securityContext": map[string]any{
				"privileged": false, "allowPrivilegeEscalation": false,
				"capabilities": map[string]any{"drop": []string{"ALL"}},
			},
		}},
	}
	if len(config.ImagePullSecrets) != 0 {
		refs := make([]map[string]string, 0, len(config.ImagePullSecrets))
		for _, name := range config.ImagePullSecrets {
			refs = append(refs, map[string]string{"name": name})
		}
		body["imagePullSecrets"] = refs
	}
	data, err := json.Marshal(body)
	if err != nil {
		return Pod{}, errors.New("encode Kubernetes Pod specification")
	}
	sum := sha256.Sum256([]byte(spec.RequestID))
	requestLabel := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:]))
	return Pod{APIVersion: "v1", Kind: "Pod", Spec: data, Metadata: Metadata{
		Name: name, Namespace: config.Namespace,
		Labels: map[string]string{
			"app.kubernetes.io/managed-by":     "commander-spawner",
			"ai.defos.commander.spawn-owner":   spec.Owner,
			"ai.defos.commander.spawn-request": requestLabel,
			"ai.defos.commander.agent-id":      spec.AgentID,
		},
		Annotations: map[string]string{
			"ai.defos.commander.spawn-request": spec.RequestID,
			"ai.defos.commander.package":       spec.Package,
			"ai.defos.commander.image":         spec.Image,
		},
	}}, nil
}

func validatePodLaunch(spec Spec) error {
	commandID, ok := strings.CutPrefix(spec.RequestID, "command:")
	if !ok || !id.Valid(commandID) || !id.Valid(spec.Owner) {
		return errors.New("Kubernetes launch requires a command UUID and owner UUID")
	}
	kind, valid := id.RuntimeType(spec.AgentID, id.Agent)
	if !valid || kind != strings.TrimSuffix(spec.AgentType, "-agent") {
		return errors.New("Kubernetes launch agent ID must match the manifest type")
	}
	ref, err := packages.ParseReference(spec.Package)
	if err != nil || ref.String() != spec.Package {
		return errors.New("Kubernetes launch requires an exact canonical agent package")
	}
	if strings.EqualFold(ref.Owner, "defos-ai") && strings.EqualFold(ref.Repository, "commander") && ref.ModulePath == "apps/agents/builder" {
		return errors.New("Kubernetes Builder spawning is not implemented")
	}
	definition := manifest.Manifest{SchemaVersion: 1, Type: spec.AgentType, Version: ref.Version, Docker: &manifest.Docker{Image: spec.Image}}
	if definition.Validate() != nil {
		return errors.New("Kubernetes launch requires a valid manifest type and digest-pinned image")
	}
	return nil
}

func validPodDNSName(value string) bool {
	return len(value) <= 253 && podDNSSubdomainPattern.MatchString(value)
}

func podEnvironment(config Config, agentID string) []map[string]any {
	suffix := agentID[strings.LastIndexByte(agentID, '-')+1:]
	env := []map[string]any{{"name": "COMMANDER_AGENT_ID_SUFFIX", "value": suffix}}
	for _, key := range []string{"BUS_URL", "STORAGE_URL"} {
		env = append(env, map[string]any{"name": key, "valueFrom": map[string]any{
			"secretKeyRef": map[string]any{"name": config.Secret, "key": key, "optional": false},
		}})
	}
	for _, key := range []string{"AWS_REGION", "AWS_ENDPOINT_URL_S3", "S3_USE_PATH_STYLE"} {
		env = append(env, map[string]any{"name": key, "valueFrom": map[string]any{
			"configMapKeyRef": map[string]any{"name": config.ConfigMap, "key": key, "optional": true},
		}})
	}
	return env
}
