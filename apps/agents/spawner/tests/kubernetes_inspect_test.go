package packages_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/defos-ai/commander/apps/agents/spawner/runtimes/kubernetes"
)

func kubernetesInspectionPods(t *testing.T) (kubernetes.Pod, kubernetes.Pod) {
	t.Helper()
	expected, err := kubernetes.BuildPod(kubernetesPodConfig(), kubernetesPodSpec())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(expected)
	if err != nil {
		t.Fatal(err)
	}
	var actual kubernetes.Pod
	if err := json.Unmarshal(raw, &actual); err != nil {
		t.Fatal(err)
	}
	actual.Metadata.UID, actual.Metadata.ResourceVersion = "pod-uid", "1"
	actual.Status = json.RawMessage(`{"phase":"Pending"}`)
	return expected, actual
}

func changeKubernetesJSON(t *testing.T, raw *json.RawMessage, change func(map[string]any)) {
	t.Helper()
	var object map[string]any
	if err := json.Unmarshal(*raw, &object); err != nil {
		t.Fatal(err)
	}
	change(object)
	updated, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	*raw = updated
}

func inspectedContainer(spec map[string]any) map[string]any {
	return spec["containers"].([]any)[0].(map[string]any)
}

func TestKubernetesInspectPodAcceptsGatedAndRunningInstances(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*testing.T, *kubernetes.Pod)
		want   kubernetes.PodState
	}{
		{"gated Pending", func(*testing.T, *kubernetes.Pod) {}, kubernetes.PodState{Gated: true, Phase: "Pending"}},
		{"additional admission gate", func(t *testing.T, actual *kubernetes.Pod) {
			changeKubernetesJSON(t, &actual.Spec, func(spec map[string]any) {
				spec["schedulingGates"] = append(spec["schedulingGates"].([]any), map[string]any{"name": "admission.example/wait"})
			})
		}, kubernetes.PodState{Gated: true, Phase: "Pending"}},
		{"ungated image pull wait", func(t *testing.T, actual *kubernetes.Pod) {
			changeKubernetesJSON(t, &actual.Spec, func(spec map[string]any) { delete(spec, "schedulingGates") })
			actual.Status = json.RawMessage(`{"phase":"Pending","containerStatuses":[{"name":"agent","restartCount":0,"state":{"waiting":{"reason":"ImagePullBackOff"}}}]}`)
		}, kubernetes.PodState{Phase: "Pending"}},
		{"running fingerprint", func(t *testing.T, actual *kubernetes.Pod) {
			changeKubernetesJSON(t, &actual.Spec, func(spec map[string]any) {
				delete(spec, "schedulingGates")
				spec["nodeName"] = "worker.example"
			})
			actual.Status = json.RawMessage(`{"phase":"Running","podIP":"10.0.0.1","containerStatuses":[{"name":"agent","restartCount":0,"ready":true,"started":true,"containerID":"containerd://first","state":{"running":{"startedAt":"2026-10-27T10:00:00.123Z"}},"lastState":{}}]}`)
		}, kubernetes.PodState{Phase: "Running", ContainerID: "containerd://first", StartedAt: "2026-10-27T10:00:00.123Z"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			expected, actual := kubernetesInspectionPods(t)
			test.change(t, &actual)
			actual.Metadata.Labels["admission.example/label"] = "unrelated"
			actual.Metadata.Annotations["admission.example/note"] = "unrelated"
			beforeExpected, _ := json.Marshal(expected)
			beforeActual, _ := json.Marshal(actual)
			state, err := kubernetes.InspectPod(expected, actual)
			if err != nil || state != test.want {
				t.Fatalf("inspect Pod: got %+v (%v), want %+v", state, err, test.want)
			}
			afterExpected, _ := json.Marshal(expected)
			afterActual, _ := json.Marshal(actual)
			if string(beforeExpected) != string(afterExpected) || string(beforeActual) != string(afterActual) {
				t.Fatal("inspection mutated its expected or observed Pod")
			}
		})
	}
}

func TestKubernetesInspectPodAcceptsKnownDefaultsAndEquivalentResources(t *testing.T) {
	expected, actual := kubernetesInspectionPods(t)
	changeKubernetesJSON(t, &actual.Spec, func(spec map[string]any) {
		spec["dnsPolicy"], spec["schedulerName"] = "ClusterFirst", "default-scheduler"
		spec["terminationGracePeriodSeconds"], spec["priority"], spec["preemptionPolicy"] = 30, 0, "PreemptLowerPriority"
		spec["serviceAccount"] = "agent-worker"
		spec["hostNetwork"], spec["hostPID"], spec["hostIPC"], spec["shareProcessNamespace"] = false, false, false, false
		spec["tolerations"] = []any{
			map[string]any{"key": "node.kubernetes.io/not-ready", "operator": "Exists", "effect": "NoExecute", "tolerationSeconds": 300},
			map[string]any{"key": "node.kubernetes.io/unreachable", "operator": "Exists", "effect": "NoExecute", "tolerationSeconds": 300},
		}
		container := inspectedContainer(spec)
		container["terminationMessagePath"], container["terminationMessagePolicy"] = "/dev/termination-log", "File"
		container["stdin"], container["stdinOnce"], container["tty"] = false, false, false
		resources := container["resources"].(map[string]any)
		resources["requests"].(map[string]any)["cpu"] = "0.25"
		resources["requests"].(map[string]any)["memory"] = "268435456"
		resources["limits"].(map[string]any)["cpu"] = "1e0"
		resources["limits"].(map[string]any)["memory"] = "1024Mi"
	})
	if _, err := kubernetes.InspectPod(expected, actual); err != nil {
		t.Fatalf("standard Kubernetes defaults changed launch ownership: %v", err)
	}
	for _, test := range []struct{ before, after string }{{"5e-1", "500m"}, {"1.5Gi", "1536Mi"}, {"100000000n", "100m"}, {"1G", "1000M"}} {
		t.Run(test.before+"="+test.after, func(t *testing.T) {
			expected, actual := kubernetesInspectionPods(t)
			for _, edit := range []struct {
				pod   *kubernetes.Pod
				value string
			}{{&expected, test.before}, {&actual, test.after}} {
				changeKubernetesJSON(t, &edit.pod.Spec, func(spec map[string]any) {
					inspectedContainer(spec)["resources"].(map[string]any)["requests"].(map[string]any)["cpu"] = edit.value
				})
			}
			if _, err := kubernetes.InspectPod(expected, actual); err != nil {
				t.Fatalf("equivalent exact resource values were rejected: %v", err)
			}
		})
	}
}

func TestKubernetesInspectPodRejectsIdentityChanges(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*kubernetes.Pod)
	}{
		{"API version", func(p *kubernetes.Pod) { p.APIVersion = "other" }},
		{"kind", func(p *kubernetes.Pod) { p.Kind = "Job" }},
		{"namespace", func(p *kubernetes.Pod) { p.Metadata.Namespace = "other" }},
		{"name", func(p *kubernetes.Pod) { p.Metadata.Name = "other" }},
		{"missing UID", func(p *kubernetes.Pod) { p.Metadata.UID = "" }},
		{"missing revision", func(p *kubernetes.Pod) { p.Metadata.ResourceVersion = "" }},
		{"deleting", func(p *kubernetes.Pod) { value := "2026-10-27T10:00:00Z"; p.Metadata.DeletionTimestamp = &value }},
		{"owner reference", func(p *kubernetes.Pod) {
			p.Metadata.OwnerReferences = []json.RawMessage{json.RawMessage(`{"uid":"another-owner"}`)}
		}},
		{"finalizer", func(p *kubernetes.Pod) { p.Metadata.Finalizers = []string{"other.example/retain"} }},
		{"ownership label", func(p *kubernetes.Pod) {
			p.Metadata.Labels["ai.defos.commander.spawn-owner"] = "private-value-sentinel"
		}},
		{"missing label", func(p *kubernetes.Pod) { delete(p.Metadata.Labels, "ai.defos.commander.agent-id") }},
		{"package annotation", func(p *kubernetes.Pod) {
			p.Metadata.Annotations["ai.defos.commander.package"] = "private-value-sentinel"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			expected, actual := kubernetesInspectionPods(t)
			test.change(&actual)
			if _, err := kubernetes.InspectPod(expected, actual); err == nil || strings.Contains(err.Error(), "private-value-sentinel") {
				t.Fatal("identity mutation was accepted or disclosed an observed value")
			}
		})
	}
}

func TestKubernetesInspectPodRejectsAdmittedSpecMutations(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(map[string]any)
	}{
		{"sidecar", func(s map[string]any) {
			s["containers"] = append(s["containers"].([]any), map[string]any{"name": "other"})
		}},
		{"init container", func(s map[string]any) { s["initContainers"] = []any{map[string]any{"name": "init"}} }},
		{"ephemeral container", func(s map[string]any) { s["ephemeralContainers"] = []any{map[string]any{"name": "debug"}} }},
		{"token automount", func(s map[string]any) { s["automountServiceAccountToken"] = true }},
		{"projected token volume", func(s map[string]any) {
			s["volumes"] = []any{map[string]any{"name": "token", "projected": map[string]any{}}}
		}},
		{"host network", func(s map[string]any) { s["hostNetwork"] = true }},
		{"host PID", func(s map[string]any) { s["hostPID"] = true }},
		{"host IPC", func(s map[string]any) { s["hostIPC"] = true }},
		{"shared processes", func(s map[string]any) { s["shareProcessNamespace"] = true }},
		{"ServiceAccount", func(s map[string]any) { s["serviceAccountName"] = "privileged" }},
		{"ServiceAccount alias", func(s map[string]any) { s["serviceAccount"] = "privileged" }},
		{"DNS policy", func(s map[string]any) { s["dnsPolicy"] = "Default" }},
		{"scheduler", func(s map[string]any) { s["schedulerName"] = "other" }},
		{"grace period", func(s map[string]any) { s["terminationGracePeriodSeconds"] = 0 }},
		{"Linux OS", func(s map[string]any) { s["os"] = map[string]any{"name": "windows"} }},
		{"Linux selector", func(s map[string]any) { s["nodeSelector"] = map[string]any{"kubernetes.io/os": "windows"} }},
		{"bound while gated", func(s map[string]any) { s["nodeName"] = "bound-node" }},
		{"run-as user", func(s map[string]any) { s["securityContext"].(map[string]any)["runAsUser"] = 0 }},
		{"run-as group", func(s map[string]any) { s["securityContext"].(map[string]any)["runAsGroup"] = 0 }},
		{"seccomp", func(s map[string]any) {
			s["securityContext"].(map[string]any)["seccompProfile"] = map[string]any{"type": "Unconfined"}
		}},
		{"privileged", func(s map[string]any) { inspectedContainer(s)["securityContext"].(map[string]any)["privileged"] = true }},
		{"escalation", func(s map[string]any) {
			inspectedContainer(s)["securityContext"].(map[string]any)["allowPrivilegeEscalation"] = true
		}},
		{"added capability", func(s map[string]any) {
			inspectedContainer(s)["securityContext"].(map[string]any)["capabilities"].(map[string]any)["add"] = []any{"SYS_ADMIN"}
		}},
		{"container user override", func(s map[string]any) { inspectedContainer(s)["securityContext"].(map[string]any)["runAsUser"] = 0 }},
		{"image", func(s map[string]any) { inspectedContainer(s)["image"] = "private-value-sentinel" }},
		{"command", func(s map[string]any) { inspectedContainer(s)["command"] = []any{"private-value-sentinel"} }},
		{"args", func(s map[string]any) { inspectedContainer(s)["args"] = []any{"private-value-sentinel"} }},
		{"envFrom", func(s map[string]any) {
			inspectedContainer(s)["envFrom"] = []any{map[string]any{"secretRef": map[string]any{"name": "all-secrets"}}}
		}},
		{"extra environment", func(s map[string]any) {
			c := inspectedContainer(s)
			c["env"] = append(c["env"].([]any), map[string]any{"name": "GITHUB_TOKEN", "value": "private-value-sentinel"})
		}},
		{"Secret reference", func(s map[string]any) {
			inspectedContainer(s)["env"].([]any)[1].(map[string]any)["valueFrom"] = map[string]any{"secretKeyRef": map[string]any{"name": "other", "key": "BUS_URL"}}
		}},
		{"resource change", func(s map[string]any) {
			inspectedContainer(s)["resources"].(map[string]any)["requests"].(map[string]any)["cpu"] = "2"
		}},
		{"invalid resource", func(s map[string]any) {
			inspectedContainer(s)["resources"].(map[string]any)["limits"].(map[string]any)["memory"] = "private-value-sentinel"
		}},
		{"extra resource", func(s map[string]any) {
			inspectedContainer(s)["resources"].(map[string]any)["limits"].(map[string]any)["nvidia.com/gpu"] = "1"
		}},
		{"unknown spec field", func(s map[string]any) { s["futureField"] = "private-value-sentinel" }},
		{"unknown container field", func(s map[string]any) { inspectedContainer(s)["futureField"] = "private-value-sentinel" }},
		{"unknown security field", func(s map[string]any) { s["securityContext"].(map[string]any)["futureField"] = true }},
		{"duplicate owned gate", func(s map[string]any) {
			s["schedulingGates"] = append(s["schedulingGates"].([]any), map[string]any{"name": kubernetes.SchedulingGate})
		}},
		{"gate unknown field", func(s map[string]any) { s["schedulingGates"].([]any)[0].(map[string]any)["other"] = true }},
		{"broad toleration", func(s map[string]any) { s["tolerations"] = []any{map[string]any{"operator": "Exists"}} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			expected, actual := kubernetesInspectionPods(t)
			changeKubernetesJSON(t, &actual.Spec, test.change)
			if _, err := kubernetes.InspectPod(expected, actual); err == nil || strings.Contains(err.Error(), "private-value-sentinel") {
				t.Fatal("admitted mutation was accepted or disclosed an observed value")
			}
		})
	}
}

func TestKubernetesInspectPodRejectsPriorExecutionAndInvalidStatus(t *testing.T) {
	for _, test := range []struct {
		name, status string
		ungated      bool
	}{
		{"missing phase", `{}`, false},
		{"Succeeded", `{"phase":"Succeeded"}`, true},
		{"Failed", `{"phase":"Failed"}`, true},
		{"Unknown", `{"phase":"Unknown"}`, true},
		{"Running without status", `{"phase":"Running"}`, true},
		{"two containers", `{"phase":"Pending","containerStatuses":[{"name":"agent"},{"name":"sidecar"}]}`, false},
		{"wrong container", `{"phase":"Pending","containerStatuses":[{"name":"other","restartCount":0,"state":{}}]}`, false},
		{"restart", `{"phase":"Pending","containerStatuses":[{"name":"agent","restartCount":1,"state":{"waiting":{}}}]}`, true},
		{"previous termination", `{"phase":"Pending","containerStatuses":[{"name":"agent","restartCount":0,"state":{"waiting":{}},"lastState":{"terminated":{"exitCode":0}}}]}`, true},
		{"terminated now", `{"phase":"Pending","containerStatuses":[{"name":"agent","restartCount":0,"state":{"terminated":{"exitCode":0}}}]}`, true},
		{"gated running", `{"phase":"Running","containerStatuses":[{"name":"agent","restartCount":0,"containerID":"containerd://first","state":{"running":{"startedAt":"2026-10-27T10:00:00Z"}}}]}`, false},
		{"Pending with running state", `{"phase":"Pending","containerStatuses":[{"name":"agent","restartCount":0,"containerID":"containerd://first","state":{"running":{"startedAt":"2026-10-27T10:00:00Z"}}}]}`, true},
		{"gated started flag", `{"phase":"Pending","containerStatuses":[{"name":"agent","restartCount":0,"started":true,"state":{"waiting":{}}}]}`, false},
		{"gated container ID", `{"phase":"Pending","containerStatuses":[{"name":"agent","restartCount":0,"containerID":"containerd://first","state":{"waiting":{}}}]}`, false},
		{"missing container ID", `{"phase":"Running","containerStatuses":[{"name":"agent","restartCount":0,"state":{"running":{"startedAt":"2026-10-27T10:00:00Z"}}}]}`, true},
		{"missing timestamp", `{"phase":"Running","containerStatuses":[{"name":"agent","restartCount":0,"containerID":"containerd://first","state":{"running":{}}}]}`, true},
		{"invalid timestamp", `{"phase":"Running","containerStatuses":[{"name":"agent","restartCount":0,"containerID":"containerd://first","state":{"running":{"startedAt":"private-value-sentinel"}}}]}`, true},
		{"mixed states", `{"phase":"Running","containerStatuses":[{"name":"agent","restartCount":0,"containerID":"containerd://first","state":{"running":{"startedAt":"2026-10-27T10:00:00Z"},"waiting":{}}}]}`, true},
		{"unexpected init execution", `{"phase":"Pending","initContainerStatuses":[{"name":"init","state":{"terminated":{}}}]}`, false},
		{"unexpected ephemeral execution", `{"phase":"Pending","ephemeralContainerStatuses":[{"name":"debug"}]}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			expected, actual := kubernetesInspectionPods(t)
			if test.ungated {
				changeKubernetesJSON(t, &actual.Spec, func(spec map[string]any) { delete(spec, "schedulingGates") })
			}
			actual.Status = json.RawMessage(test.status)
			state, err := kubernetes.InspectPod(expected, actual)
			if err == nil || !reflect.DeepEqual(state, kubernetes.PodState{}) || strings.Contains(err.Error(), "private-value-sentinel") {
				t.Fatal("invalid execution state returned a usable fingerprint or disclosed an observed value")
			}
		})
	}
}

func TestKubernetesInspectPodRejectsDuplicateSpecKeys(t *testing.T) {
	expected, actual := kubernetesInspectionPods(t)
	actual.Spec = json.RawMessage(`{"hostNetwork":true,` + string(actual.Spec[1:len(actual.Spec)-1]) + `,"hostNetwork":false}`)
	if _, err := kubernetes.InspectPod(expected, actual); err == nil {
		t.Fatal("inspection accepted duplicate security-related JSON fields")
	}
}
