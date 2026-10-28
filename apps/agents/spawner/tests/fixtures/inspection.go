package fixtures

import (
	"encoding/json"
	"testing"

	"github.com/defos-ai/commander/apps/agents/spawner/runtimes/kubernetes"
)

func InspectionPods(t *testing.T) (kubernetes.Pod, kubernetes.Pod) {
	t.Helper()
	expected, err := kubernetes.BuildPod(PodConfig(), PodSpec())
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

func ChangePodJSON(t *testing.T, raw *json.RawMessage, change func(map[string]any)) {
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

func InspectedContainer(spec map[string]any) map[string]any {
	return spec["containers"].([]any)[0].(map[string]any)
}
