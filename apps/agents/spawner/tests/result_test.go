package packages_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/defos-ai/commander/apps/agents/spawner/runtimes"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
)

func TestRuntimeKindJSON(t *testing.T) {
	for _, kind := range []runtimes.RuntimeKind{runtimes.RuntimeNative, runtimes.RuntimeDocker, runtimes.RuntimeKubernetes} {
		t.Run(string(kind), func(t *testing.T) {
			raw, err := json.Marshal(kind)
			if err != nil {
				t.Fatal(err)
			}
			if string(raw) != `"`+string(kind)+`"` {
				t.Fatalf("runtime kind did not encode as its string value: %s", raw)
			}
			var actual runtimes.RuntimeKind
			if err := json.Unmarshal(raw, &actual); err != nil || actual != kind {
				t.Fatalf("runtime kind did not round trip: %q (%v)", actual, err)
			}
		})
	}
}

func TestSpawnResultJSONRoundTrip(t *testing.T) {
	for _, test := range []struct {
		kind    runtimes.RuntimeKind
		details runtimes.RuntimeDetails
		wire    string
	}{
		{runtimes.RuntimeNative, runtimes.NativeRuntimeDetails{PID: 42}, `{"pid":42}`},
		{runtimes.RuntimeDocker, runtimes.DockerRuntimeDetails{ContainerID: "fixture-container"}, `{"container_id":"fixture-container"}`},
		{runtimes.RuntimeKubernetes, runtimes.KubernetesRuntimeDetails{Namespace: "agents", PodName: "worker.example-1", PodUID: "opaque:pod/uid"}, `{"namespace":"agents","pod_name":"worker.example-1","pod_uid":"opaque:pod/uid"}`},
	} {
		t.Run(string(test.kind), func(t *testing.T) {
			want := runtimes.Result{
				AgentID: "example-agent-12345678", AgentType: "example-agent", Package: actionPackage,
				Runtime: test.kind, RuntimeDetails: test.details, Actions: []actionmodel.Action{},
			}
			raw, err := json.Marshal(want)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(raw, &fields); err != nil {
				t.Fatal(err)
			}
			var actualDetails, wantDetails map[string]any
			if err := json.Unmarshal(fields["runtime_details"], &actualDetails); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(test.wire), &wantDetails); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(actualDetails, wantDetails) {
				t.Fatalf("runtime details changed the public JSON shape: %s", fields["runtime_details"])
			}
			for _, name := range []string{"pid", "container_id", "namespace", "pod_name", "pod_uid"} {
				if _, present := fields[name]; present {
					t.Fatalf("result exposed obsolete flat runtime field %s", name)
				}
			}
			var actual runtimes.Result
			if err := json.Unmarshal(raw, &actual); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(actual, want) {
				t.Fatalf("result lost its concrete runtime details: got %#v, want %#v", actual, want)
			}
		})
	}
}

func TestSpawnResultJSONRejectsInvalidRuntimeDetails(t *testing.T) {
	for _, test := range []struct {
		name      string
		overrides map[string]any
		omit      string
	}{
		{"missing runtime", nil, "runtime"},
		{"null runtime", map[string]any{"runtime": nil}, ""},
		{"numeric runtime", map[string]any{"runtime": 42}, ""},
		{"unsupported runtime", map[string]any{"runtime": "job"}, ""},
		{"missing details", nil, "runtime_details"},
		{"null details", map[string]any{"runtime_details": nil}, ""},
		{"array details", map[string]any{"runtime_details": []any{42}}, ""},
		{"string details", map[string]any{"runtime_details": "pid=42"}, ""},
		{"numeric details", map[string]any{"runtime_details": 42}, ""},
		{"boolean details", map[string]any{"runtime_details": true}, ""},
		{"unknown outer field", map[string]any{"other": true}, ""},
		{"unknown nested field", map[string]any{"runtime_details": map[string]any{"pid": 42, "other": true}}, ""},
		{"native with docker details", map[string]any{"runtime_details": map[string]any{"container_id": "container"}}, ""},
		{"native with mixed details", map[string]any{"runtime_details": map[string]any{"pid": 42, "namespace": "agents"}}, ""},
		{"docker with native details", map[string]any{"runtime": "docker"}, ""},
		{"kubernetes with native details", map[string]any{"runtime": "kubernetes"}, ""},
		{"extra runtime wrapper", map[string]any{"runtime_details": map[string]any{"native": map[string]any{"pid": 42}}}, ""},
		{"old flat pid", map[string]any{"pid": 42}, ""},
		{"old flat container", map[string]any{"container_id": "container"}, ""},
		{"old flat namespace", map[string]any{"namespace": "agents"}, ""},
		{"old flat pod name", map[string]any{"pod_name": "worker"}, ""},
		{"old flat pod UID", map[string]any{"pod_uid": "uid"}, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			fields := map[string]any{
				"agent_id": "example-agent-12345678", "agent_type": "example-agent", "package": actionPackage,
				"runtime": "native", "runtime_details": map[string]any{"pid": 42}, "actions": []any{},
			}
			for name, value := range test.overrides {
				fields[name] = value
			}
			delete(fields, test.omit)
			raw, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			before := runtimes.Result{AgentID: "previous-agent-12345678", Runtime: runtimes.RuntimeDocker,
				RuntimeDetails: runtimes.DockerRuntimeDetails{ContainerID: "previous-container"}}
			actual := before
			if err := json.Unmarshal(raw, &actual); err == nil {
				t.Fatal("result accepted an invalid runtime payload")
			}
			if !reflect.DeepEqual(actual, before) {
				t.Fatal("failed decoding changed the previous result")
			}
		})
	}
}
