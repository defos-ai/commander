package fixtures

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/defos-ai/commander/apps/agents/spawner/runtimes/kubernetes"
	"github.com/defos-ai/commander/apps/agents/spawner/tests/fakes"
)

const PodJSON = `{"apiVersion":"v1","kind":"Pod","metadata":{"name":"fixture-agent-a1b2c3d4","namespace":"agents","uid":"opaque:pod/uid","resourceVersion":"opaque-rv:7/+"},"spec":{"restartPolicy":"Never","schedulingGates":[{"name":"admission.example/first"},{"name":"ai.defos.commander/launch"},{"name":"admission.example/last"}],"containers":[{"name":"agent","image":"example@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}],"admittedField":{"retained":true}},"status":{"phase":"Pending","admittedStatus":{"retained":true}}}`

func Pod(t *testing.T) kubernetes.Pod {
	t.Helper()
	var pod kubernetes.Pod
	if err := json.Unmarshal([]byte(PodJSON), &pod); err != nil {
		t.Fatal(err)
	}
	return pod
}

func CreatePod(t *testing.T) kubernetes.Pod {
	t.Helper()
	pod := Pod(t)
	pod.Metadata.UID, pod.Metadata.ResourceVersion = "", ""
	pod.Status = nil
	return pod
}

func KubernetesClient(t *testing.T, handler fakes.Transport) *kubernetes.Client {
	t.Helper()
	client, err := kubernetes.NewClient("agents", "https://kubernetes.invalid:6443", &http.Client{Transport: handler})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	})
	return client
}

func KubernetesResponse(request *http.Request, status int, body string) *http.Response {
	result := Response(request, status, body)
	result.Header.Set("Content-Type", "application/json")
	return result
}
