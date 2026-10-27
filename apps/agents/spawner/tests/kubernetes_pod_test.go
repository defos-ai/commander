package packages_test

import (
	"bytes"
	"encoding/json"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/defos-ai/commander/apps/agents/spawner/runtimes/kubernetes"
)

func kubernetesPodConfig() kubernetes.Config {
	return kubernetes.Config{
		Namespace: "agents", ServiceAccount: "agent-worker", ConfigMap: "provider-config", Secret: "provider-secrets",
		ImagePullSecrets: []string{"registry-pull"}, RunAsUser: 1000, RunAsGroup: 1001,
		Resources: kubernetes.ResourceRequirements{
			Requests: map[string]string{"cpu": "250m", "memory": "256Mi"},
			Limits:   map[string]string{"cpu": "1", "memory": "1Gi"},
		},
	}
}

func kubernetesPodSpec() kubernetes.Spec {
	return kubernetes.Spec{
		RequestID: "command:11111111-1111-4111-8111-111111111111", Owner: "22222222-2222-4222-8222-222222222222",
		AgentID: "example-agent-12345678", AgentType: "example-agent", Package: actionPackage,
		Image: "ghcr.io/defos-ai/example-agent@sha256:" + strings.Repeat("a", 64),
	}
}

func TestKubernetesPodUsesLiteralPrefix(t *testing.T) {
	for _, prefix := range []string{"", "team-", "team."} {
		t.Run("prefix="+prefix, func(t *testing.T) {
			config, spec := kubernetesPodConfig(), kubernetesPodSpec()
			config.PodPrefix = prefix
			pod, err := kubernetes.BuildPod(config, spec)
			if err != nil {
				t.Fatal(err)
			}
			if pod.Metadata.Name != prefix+spec.AgentID || pod.Metadata.Namespace != config.Namespace {
				t.Fatalf("Pod destination changed the literal prefix or namespace: %+v", pod.Metadata)
			}
		})
	}
}

func TestKubernetesPodAcceptsMaximumNames(t *testing.T) {
	config, spec := kubernetesPodConfig(), kubernetesPodSpec()
	config.Namespace = strings.Repeat("a", 63)
	config.PodPrefix = strings.Repeat("b", 253-len(spec.AgentID))
	config.ServiceAccount, config.ConfigMap, config.Secret = "agent.worker", "provider.config", "provider.secret"
	pod, err := kubernetes.BuildPod(config, spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(pod.Metadata.Name) != 253 || pod.Metadata.Namespace != config.Namespace {
		t.Fatal("Pod builder rejected or changed Kubernetes name boundaries")
	}
}

func TestKubernetesPodBuildsGatedWorker(t *testing.T) {
	for _, key := range []string{"GITHUB_TOKEN", "SECRETS_ENCRYPTION_KEY", "OPENAI_API_KEY", "AWS_SECRET_ACCESS_KEY", "BUS_URL", "STORAGE_URL"} {
		t.Setenv(key, "private-parent-environment-sentinel")
	}
	config, spec := kubernetesPodConfig(), kubernetesPodSpec()
	pod, err := kubernetes.BuildPod(config, spec)
	if err != nil {
		t.Fatal(err)
	}
	if pod.APIVersion != "v1" || pod.Kind != "Pod" || pod.Metadata.UID != "" || pod.Metadata.ResourceVersion != "" ||
		len(pod.Metadata.OwnerReferences) != 0 || len(pod.Metadata.Finalizers) != 0 || pod.Metadata.DeletionTimestamp != nil || len(pod.Status) != 0 {
		t.Fatalf("builder set server identity or external lifecycle ownership: %+v", pod.Metadata)
	}
	if pod.Metadata.Labels["app.kubernetes.io/managed-by"] != "commander-spawner" || pod.Metadata.Labels["ai.defos.commander.spawn-owner"] != spec.Owner || pod.Metadata.Labels["ai.defos.commander.agent-id"] != spec.AgentID {
		t.Fatal("Pod lost its deployment ownership labels")
	}
	labelValue := regexp.MustCompile(`^[a-zA-Z0-9]([-a-zA-Z0-9_.]*[a-zA-Z0-9])?$`)
	for key, value := range pod.Metadata.Labels {
		if len(value) > 63 || !labelValue.MatchString(value) {
			t.Fatalf("label %s is not a Kubernetes label value", key)
		}
	}
	if len(pod.Metadata.Labels["ai.defos.commander.spawn-request"]) != 52 || pod.Metadata.Annotations["ai.defos.commander.spawn-request"] != spec.RequestID || pod.Metadata.Annotations["ai.defos.commander.package"] != spec.Package || pod.Metadata.Annotations["ai.defos.commander.image"] != spec.Image {
		t.Fatal("Pod did not preserve full launch identity separately from compact labels")
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(pod.Spec, &body); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"serviceAccountName": `"agent-worker"`, "automountServiceAccountToken": "false",
		"enableServiceLinks": "false", "restartPolicy": `"Never"`,
		"schedulingGates":  `[{"name":"ai.defos.commander/launch"}]`,
		"imagePullSecrets": `[{"name":"registry-pull"}]`,
		"os":               `{"name":"linux"}`,
		"nodeSelector":     `{"kubernetes.io/os":"linux"}`,
	} {
		if string(body[key]) != want {
			t.Fatalf("Pod %s: got %s, want %s", key, body[key], want)
		}
	}
	for _, key := range []string{"hostNetwork", "hostPID", "hostIPC", "volumes", "initContainers", "ephemeralContainers", "nodeName", "hostname"} {
		if _, present := body[key]; present {
			t.Fatalf("Pod unexpectedly configured %s", key)
		}
	}
	var security struct {
		RunAsNonRoot bool                  `json:"runAsNonRoot"`
		RunAsUser    int64                 `json:"runAsUser"`
		RunAsGroup   int64                 `json:"runAsGroup"`
		Seccomp      struct{ Type string } `json:"seccompProfile"`
	}
	if err := json.Unmarshal(body["securityContext"], &security); err != nil {
		t.Fatal(err)
	}
	if !security.RunAsNonRoot || security.RunAsUser != config.RunAsUser || security.RunAsGroup != config.RunAsGroup || security.Seccomp.Type != "RuntimeDefault" {
		t.Fatal("Pod lost explicit nonroot and seccomp settings")
	}
	var containers []map[string]json.RawMessage
	if err := json.Unmarshal(body["containers"], &containers); err != nil || len(containers) != 1 {
		t.Fatalf("expected one agent container: %v", err)
	}
	container := containers[0]
	if string(container["name"]) != `"agent"` || string(container["image"]) != `"`+spec.Image+`"` || string(container["imagePullPolicy"]) != `"IfNotPresent"` {
		t.Fatal("container did not use the selected digest-pinned image")
	}
	for _, key := range []string{"envFrom", "volumeMounts", "command", "args"} {
		if _, present := container[key]; present {
			t.Fatalf("agent container unexpectedly configured %s", key)
		}
	}
	var containerSecurity struct {
		Privileged               *bool                   `json:"privileged"`
		AllowPrivilegeEscalation *bool                   `json:"allowPrivilegeEscalation"`
		Capabilities             struct{ Drop []string } `json:"capabilities"`
	}
	if err := json.Unmarshal(container["securityContext"], &containerSecurity); err != nil {
		t.Fatal(err)
	}
	if containerSecurity.Privileged == nil || *containerSecurity.Privileged || containerSecurity.AllowPrivilegeEscalation == nil || *containerSecurity.AllowPrivilegeEscalation || !reflect.DeepEqual(containerSecurity.Capabilities.Drop, []string{"ALL"}) {
		t.Fatal("container granted privileges or retained capabilities")
	}
	var resources kubernetes.ResourceRequirements
	if err := json.Unmarshal(container["resources"], &resources); err != nil || !reflect.DeepEqual(resources, config.Resources) {
		t.Fatalf("container changed configured resource quantities: %+v (%v)", resources, err)
	}
	assertKubernetesPodEnvironment(t, container["env"], config)
	raw, err := json.Marshal(pod)
	if err != nil || bytes.Contains(raw, []byte("private-parent-environment-sentinel")) {
		t.Fatal("Pod inherited credentials from its parent process")
	}
}

func assertKubernetesPodEnvironment(t *testing.T, raw json.RawMessage, config kubernetes.Config) {
	t.Helper()
	var env []struct {
		Name      string `json:"name"`
		Value     string `json:"value"`
		ValueFrom map[string]struct {
			Name     string `json:"name"`
			Key      string `json:"key"`
			Optional bool   `json:"optional"`
		} `json:"valueFrom"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"COMMANDER_AGENT_ID_SUFFIX": "", "BUS_URL": "secretKeyRef", "STORAGE_URL": "secretKeyRef", "AWS_REGION": "configMapKeyRef", "AWS_ENDPOINT_URL_S3": "configMapKeyRef", "S3_USE_PATH_STYLE": "configMapKeyRef"}
	if len(env) != len(want) {
		t.Fatalf("unexpected number of child environment entries: %d", len(env))
	}
	for _, entry := range env {
		refKind, found := want[entry.Name]
		if !found {
			t.Fatalf("unexpected child environment entry %s", entry.Name)
		}
		delete(want, entry.Name)
		if refKind == "" {
			if entry.Value != "12345678" || len(entry.ValueFrom) != 0 {
				t.Fatal("agent suffix was not the only literal environment value")
			}
			continue
		}
		ref, found := entry.ValueFrom[refKind]
		name, optional := config.Secret, false
		if refKind == "configMapKeyRef" {
			name, optional = config.ConfigMap, true
		}
		if !found || len(entry.ValueFrom) != 1 || entry.Value != "" || ref.Name != name || ref.Key != entry.Name || ref.Optional != optional {
			t.Fatalf("child setting %s did not use the expected namespace-local key reference", entry.Name)
		}
	}
}

func TestKubernetesPodRejectsInvalidConfiguration(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*kubernetes.Config, *kubernetes.Spec)
	}{
		{"missing namespace", func(c *kubernetes.Config, _ *kubernetes.Spec) { c.Namespace = "" }},
		{"uppercase namespace", func(c *kubernetes.Config, _ *kubernetes.Spec) { c.Namespace = "Agents" }},
		{"dotted namespace", func(c *kubernetes.Config, _ *kubernetes.Spec) { c.Namespace = "agents.example" }},
		{"long namespace", func(c *kubernetes.Config, _ *kubernetes.Spec) { c.Namespace = strings.Repeat("a", 64) }},
		{"invalid prefix", func(c *kubernetes.Config, _ *kubernetes.Spec) { c.PodPrefix = "../" }},
		{"uppercase prefix", func(c *kubernetes.Config, _ *kubernetes.Spec) { c.PodPrefix = "Team-" }},
		{"oversized resulting name", func(c *kubernetes.Config, s *kubernetes.Spec) { c.PodPrefix = strings.Repeat("a", 254-len(s.AgentID)) }},
		{"missing ServiceAccount", func(c *kubernetes.Config, _ *kubernetes.Spec) { c.ServiceAccount = "" }},
		{"missing ConfigMap", func(c *kubernetes.Config, _ *kubernetes.Spec) { c.ConfigMap = "" }},
		{"missing Secret", func(c *kubernetes.Config, _ *kubernetes.Spec) { c.Secret = "" }},
		{"cross-namespace reference", func(c *kubernetes.Config, _ *kubernetes.Spec) { c.Secret = "other/secret" }},
		{"invalid pull reference", func(c *kubernetes.Config, _ *kubernetes.Spec) { c.ImagePullSecrets = []string{"other/secret"} }},
		{"missing requests", func(c *kubernetes.Config, _ *kubernetes.Spec) { c.Resources.Requests = nil }},
		{"missing CPU request", func(c *kubernetes.Config, _ *kubernetes.Spec) { delete(c.Resources.Requests, "cpu") }},
		{"missing memory request", func(c *kubernetes.Config, _ *kubernetes.Spec) { delete(c.Resources.Requests, "memory") }},
		{"missing limits", func(c *kubernetes.Config, _ *kubernetes.Spec) { c.Resources.Limits = nil }},
		{"missing CPU limit", func(c *kubernetes.Config, _ *kubernetes.Spec) { delete(c.Resources.Limits, "cpu") }},
		{"blank memory limit", func(c *kubernetes.Config, _ *kubernetes.Spec) { c.Resources.Limits["memory"] = " " }},
		{"root user", func(c *kubernetes.Config, _ *kubernetes.Spec) { c.RunAsUser = 0 }},
		{"negative group", func(c *kubernetes.Config, _ *kubernetes.Spec) { c.RunAsGroup = -1 }},
		{"invalid agent ID", func(_ *kubernetes.Config, s *kubernetes.Spec) { s.AgentID = "example-agent-not-an-id" }},
		{"mismatched agent type", func(_ *kubernetes.Config, s *kubernetes.Spec) { s.AgentType = "other-agent" }},
		{"missing command prefix", func(_ *kubernetes.Config, s *kubernetes.Spec) {
			s.RequestID = strings.TrimPrefix(s.RequestID, "command:")
		}},
		{"invalid command UUID", func(_ *kubernetes.Config, s *kubernetes.Spec) { s.RequestID = "command:invalid" }},
		{"invalid owner UUID", func(_ *kubernetes.Config, s *kubernetes.Spec) { s.Owner = "owner" }},
		{"moving package", func(_ *kubernetes.Config, s *kubernetes.Spec) { s.Package = "github.com/defos-ai/example@stable" }},
		{"package homepage", func(_ *kubernetes.Config, s *kubernetes.Spec) { s.Package = "https://github.com/defos-ai/example" }},
		{"tagged image", func(_ *kubernetes.Config, s *kubernetes.Spec) { s.Image = "ghcr.io/defos-ai/example:stable" }},
		{"tag and digest", func(_ *kubernetes.Config, s *kubernetes.Spec) {
			s.Image = "ghcr.io/defos-ai/example:stable@sha256:" + strings.Repeat("a", 64)
		}},
		{"implicit registry", func(_ *kubernetes.Config, s *kubernetes.Spec) { s.Image = "example@sha256:" + strings.Repeat("a", 64) }},
		{"invalid image digest", func(_ *kubernetes.Config, s *kubernetes.Spec) { s.Image = "ghcr.io/defos-ai/example@sha256:invalid" }},
		{"official Builder", func(_ *kubernetes.Config, s *kubernetes.Spec) {
			s.Package, s.AgentType, s.AgentID = "github.com/defos-ai/commander/apps/agents/builder@v0.2.0", "builder-agent", "builder-agent-12345678"
		}},
		{"official Builder repository case alias", func(_ *kubernetes.Config, s *kubernetes.Spec) {
			s.Package, s.AgentType, s.AgentID = "github.com/DeFoS-Ai/CoMmAnDeR/apps/agents/builder@v0.2.0", "builder-agent", "builder-agent-12345678"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			config, spec := kubernetesPodConfig(), kubernetesPodSpec()
			test.change(&config, &spec)
			if _, err := kubernetes.BuildPod(config, spec); err == nil {
				t.Fatal("invalid Kubernetes launch was accepted")
			}
		})
	}
}

func TestKubernetesPodOwnsConfigurationCopies(t *testing.T) {
	config, spec := kubernetesPodConfig(), kubernetesPodSpec()
	first, err := kubernetes.BuildPod(config, spec)
	if err != nil {
		t.Fatal(err)
	}
	second, err := kubernetes.BuildPod(config, spec)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(config, kubernetesPodConfig()) {
		t.Fatal("building a Pod mutated deployment configuration")
	}
	before := append([]byte(nil), first.Spec...)
	config.Resources.Requests["cpu"] = "2"
	config.Resources.Limits["memory"] = "8Gi"
	config.ImagePullSecrets[0] = "changed"
	if !bytes.Equal(first.Spec, before) {
		t.Fatal("mutating deployment configuration changed the constructed Pod")
	}
	first.Metadata.Labels["ai.defos.commander.spawn-owner"] = "changed"
	first.Metadata.Annotations["ai.defos.commander.package"] = "changed"
	first.Spec[0] = '['
	if second.Metadata.Labels["ai.defos.commander.spawn-owner"] != spec.Owner || second.Metadata.Annotations["ai.defos.commander.package"] != spec.Package || !bytes.Equal(second.Spec, before) {
		t.Fatal("constructed Pods share mutable identity or specification data")
	}
}

func TestKubernetesPodPreservesResourceQuantitySpelling(t *testing.T) {
	config := kubernetesPodConfig()
	config.Resources.Requests["cpu"] = "5e-1"
	config.Resources.Limits["memory"] = "1.5Gi"
	pod, err := kubernetes.BuildPod(config, kubernetesPodSpec())
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Containers []struct {
			Resources kubernetes.ResourceRequirements `json:"resources"`
		} `json:"containers"`
	}
	if err := json.Unmarshal(pod.Spec, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Containers) != 1 || !reflect.DeepEqual(body.Containers[0].Resources, config.Resources) {
		t.Fatal("Pod builder changed the operator's resource quantity spelling")
	}
}
