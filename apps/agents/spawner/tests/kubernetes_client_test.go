package packages_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/agents/spawner/runtimes/kubernetes"
)

const kubePodJSON = `{"apiVersion":"v1","kind":"Pod","metadata":{"name":"fixture-agent-a1b2c3d4","namespace":"agents","uid":"opaque:pod/uid","resourceVersion":"opaque-rv:7/+"},"spec":{"restartPolicy":"Never","schedulingGates":[{"name":"admission.example/first"},{"name":"ai.defos.commander/launch"},{"name":"admission.example/last"}],"containers":[{"name":"agent","image":"example@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}],"admittedField":{"retained":true}},"status":{"phase":"Pending","admittedStatus":{"retained":true}}}`

func kubePod(t *testing.T) kubernetes.Pod {
	t.Helper()
	var pod kubernetes.Pod
	if err := json.Unmarshal([]byte(kubePodJSON), &pod); err != nil {
		t.Fatal(err)
	}
	return pod
}

func kubeCreatePod(t *testing.T) kubernetes.Pod {
	t.Helper()
	pod := kubePod(t)
	pod.Metadata.UID, pod.Metadata.ResourceVersion = "", ""
	pod.Status = nil
	return pod
}

func kubeClient(t *testing.T, handler transportFunc) *kubernetes.Client {
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

func kubeResponse(request *http.Request, status int, body string) *http.Response {
	result := response(request, status, body)
	result.Header.Set("Content-Type", "application/json")
	return result
}

func checkKubeMutation(t *testing.T, request *http.Request) {
	t.Helper()
	if request.GetBody != nil || request.Header.Get("Idempotency-Key") != "" || request.Header.Get("X-Idempotency-Key") != "" {
		t.Fatal("mutation can be replayed by the HTTP client")
	}
	deadline, ok := request.Context().Deadline()
	if !ok || time.Until(deadline) > 31*time.Second {
		t.Fatal("Kubernetes mutation has no bounded request deadline")
	}
}

func TestKubernetesClientRejectsInvalidConfiguration(t *testing.T) {
	transport := transportFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid configuration reached the API")
		return nil, errors.New("unexpected request")
	})
	valid := &http.Client{Transport: transport}
	for _, test := range []struct {
		name, namespace, server string
		client                  *http.Client
	}{
		{"empty namespace", "", "https://kubernetes.invalid", valid},
		{"namespace traversal", "../agents", "https://kubernetes.invalid", valid},
		{"uppercase namespace", "Agents", "https://kubernetes.invalid", valid},
		{"long namespace", strings.Repeat("a", 64), "https://kubernetes.invalid", valid},
		{"insecure API", "agents", "http://kubernetes.invalid", valid},
		{"API credentials", "agents", "https://key:private-api-sentinel@kubernetes.invalid", valid},
		{"API path", "agents", "https://kubernetes.invalid/api", valid},
		{"API query", "agents", "https://kubernetes.invalid?token=private-api-sentinel", valid},
		{"API fragment", "agents", "https://kubernetes.invalid#private-api-sentinel", valid},
		{"relative API", "agents", "kubernetes.invalid", valid},
		{"missing host", "agents", "https://", valid},
		{"missing client", "agents", "https://kubernetes.invalid", nil},
		{"implicit transport", "agents", "https://kubernetes.invalid", &http.Client{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			client, err := kubernetes.NewClient(test.namespace, test.server, test.client)
			if err == nil {
				_ = client.Close()
				t.Fatal("invalid Kubernetes configuration was accepted")
			}
			if strings.Contains(err.Error(), "private-api-sentinel") {
				t.Fatal("configuration error disclosed API credentials")
			}
		})
	}
}

func TestKubernetesClientCreatesAndReadsAdmittedPod(t *testing.T) {
	want := kubePod(t)
	input := kubeCreatePod(t)
	calls := 0
	client := kubeClient(t, func(request *http.Request) (*http.Response, error) {
		calls++
		if request.URL.Scheme != "https" || request.URL.Host != "kubernetes.invalid:6443" || request.URL.RawQuery != "" {
			t.Fatalf("unexpected API destination: %s", request.URL)
		}
		if request.Header.Get("Accept") != "application/json" {
			t.Fatal("client did not request JSON")
		}
		switch calls {
		case 1:
			if request.Method != http.MethodPost || request.URL.Path != "/api/v1/namespaces/agents/pods" || request.Header.Get("Content-Type") != "application/json" {
				t.Fatalf("unexpected create request: %s %s", request.Method, request.URL)
			}
			checkKubeMutation(t, request)
			var actual kubernetes.Pod
			if err := json.NewDecoder(request.Body).Decode(&actual); err != nil || !reflect.DeepEqual(actual, input) {
				t.Fatalf("create changed the submitted Pod: %+v, %v", actual, err)
			}
			return kubeResponse(request, http.StatusCreated, kubePodJSON), nil
		case 2:
			if request.Method != http.MethodGet || request.URL.Path != "/api/v1/namespaces/agents/pods/"+want.Metadata.Name {
				t.Fatalf("unexpected get request: %s %s", request.Method, request.URL)
			}
			return kubeResponse(request, http.StatusOK, kubePodJSON), nil
		default:
			t.Fatal("create/get made an extra API request")
			return nil, errors.New("unexpected request")
		}
	})
	created, err := client.Create(t.Context(), input)
	if err != nil || !reflect.DeepEqual(created, want) {
		t.Fatalf("create lost admitted identity, spec or status: %+v, %v", created, err)
	}
	got, err := client.Get(t.Context(), want.Metadata.Name)
	if err != nil || !reflect.DeepEqual(got, want) || calls != 2 {
		t.Fatalf("get lost admitted identity, spec or status: %+v, %v", got, err)
	}
}

func TestKubernetesClientRejectsInvalidOperationsBeforeRequest(t *testing.T) {
	client := kubeClient(t, func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid Pod operation reached the API")
		return nil, errors.New("unexpected request")
	})
	for _, test := range []struct {
		name string
		edit func(*kubernetes.Pod)
	}{
		{"wrong namespace", func(p *kubernetes.Pod) { p.Metadata.Namespace = "other" }},
		{"path as name", func(p *kubernetes.Pod) { p.Metadata.Name = "../other" }},
		{"wrong kind", func(p *kubernetes.Pod) { p.Kind = "Job" }},
		{"wrong version", func(p *kubernetes.Pod) { p.APIVersion = "apps/v1" }},
		{"assigned UID", func(p *kubernetes.Pod) { p.Metadata.UID = "already-created" }},
		{"assigned version", func(p *kubernetes.Pod) { p.Metadata.ResourceVersion = "existing" }},
		{"missing gate", func(p *kubernetes.Pod) { p.Spec = json.RawMessage(`{"restartPolicy":"Never"}`) }},
		{"duplicate gate", func(p *kubernetes.Pod) {
			p.Spec = json.RawMessage(`{"restartPolicy":"Never","schedulingGates":[{"name":"ai.defos.commander/launch"},{"name":"ai.defos.commander/launch"}]}`)
		}},
		{"automatic restart", func(p *kubernetes.Pod) {
			p.Spec = json.RawMessage(`{"restartPolicy":"Always","schedulingGates":[{"name":"ai.defos.commander/launch"}]}`)
		}},
	} {
		t.Run("create/"+test.name, func(t *testing.T) {
			pod := kubeCreatePod(t)
			test.edit(&pod)
			if _, err := client.Create(t.Context(), pod); err == nil {
				t.Fatal("invalid create was accepted")
			}
		})
	}
	for _, name := range []string{"", "../other", "worker/other", "Uppercase"} {
		if _, err := client.Get(t.Context(), name); err == nil {
			t.Fatalf("get accepted invalid name %q", name)
		}
		if watch, err := client.Watch(t.Context(), name, "version"); err == nil {
			_ = watch.Close()
			t.Fatalf("watch accepted invalid name %q", name)
		}
	}
	for _, test := range []struct {
		name string
		edit func(*kubernetes.Pod)
	}{
		{"missing UID", func(p *kubernetes.Pod) { p.Metadata.UID = "" }},
		{"missing version", func(p *kubernetes.Pod) { p.Metadata.ResourceVersion = "" }},
		{"wrong namespace", func(p *kubernetes.Pod) { p.Metadata.Namespace = "other" }},
		{"missing gate", func(p *kubernetes.Pod) { p.Spec = json.RawMessage(`{"restartPolicy":"Never"}`) }},
	} {
		t.Run("release/"+test.name, func(t *testing.T) {
			pod := kubePod(t)
			test.edit(&pod)
			if _, err := client.ReleaseGate(t.Context(), pod); err == nil {
				t.Fatal("gate release accepted incomplete observed identity")
			}
		})
	}
}

func TestKubernetesClientConditionallyRemovesOnlyItsGate(t *testing.T) {
	observed := kubePod(t)
	released := strings.Replace(kubePodJSON, `{"name":"ai.defos.commander/launch"},`, "", 1)
	calls := 0
	client := kubeClient(t, func(request *http.Request) (*http.Response, error) {
		calls++
		if request.Method != http.MethodPatch || request.URL.Path != "/api/v1/namespaces/agents/pods/"+observed.Metadata.Name || request.URL.RawQuery != "" {
			t.Fatalf("unexpected gate request: %s %s", request.Method, request.URL)
		}
		if request.Header.Get("Content-Type") != "application/json-patch+json" {
			t.Fatal("gate release did not use conditional JSON Patch")
		}
		checkKubeMutation(t, request)
		var patch []map[string]any
		if err := json.NewDecoder(request.Body).Decode(&patch); err != nil {
			t.Fatal(err)
		}
		want := []map[string]any{
			{"op": "test", "path": "/metadata/uid", "value": observed.Metadata.UID},
			{"op": "test", "path": "/metadata/resourceVersion", "value": observed.Metadata.ResourceVersion},
			{"op": "test", "path": "/spec/schedulingGates/1/name", "value": kubernetes.SchedulingGate},
			{"op": "remove", "path": "/spec/schedulingGates/1"},
		}
		if !reflect.DeepEqual(patch, want) {
			t.Fatalf("gate patch could release a changed Pod or another controller's gate: %+v", patch)
		}
		return kubeResponse(request, http.StatusOK, released), nil
	})
	actual, err := client.ReleaseGate(t.Context(), observed)
	if err != nil || calls != 1 || strings.Contains(string(actual.Spec), kubernetes.SchedulingGate) ||
		!strings.Contains(string(actual.Spec), "admission.example/first") || !strings.Contains(string(actual.Spec), "admission.example/last") {
		t.Fatalf("gate release lost another gate or failed: %+v, %v", actual, err)
	}
}

func TestKubernetesClientDoesNotReplayMutationsOrDiscloseFailures(t *testing.T) {
	for _, method := range []string{"create", "release"} {
		for _, failure := range []string{"transport", "rate limit", "server error", "redirect"} {
			t.Run(method+"/"+failure, func(t *testing.T) {
				calls, redirects := 0, 0
				httpClient := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { redirects++; return nil }}
				httpClient.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
					calls++
					checkKubeMutation(t, request)
					if failure == "transport" {
						return nil, errors.New("lost response with private-api-sentinel")
					}
					status := map[string]int{"rate limit": 429, "server error": 503, "redirect": 307}[failure]
					result := kubeResponse(request, status, `{"message":"private-api-sentinel"}`)
					result.Header.Set("Retry-After", "1")
					result.Header.Set("Location", "https://another.invalid?token=private-api-sentinel")
					return result, nil
				})
				client, err := kubernetes.NewClient("agents", "https://kubernetes.invalid", httpClient)
				if err != nil {
					t.Fatal(err)
				}
				defer client.Close()
				if method == "create" {
					_, err = client.Create(t.Context(), kubeCreatePod(t))
				} else {
					_, err = client.ReleaseGate(t.Context(), kubePod(t))
				}
				if err == nil || strings.Contains(err.Error(), "private-api-sentinel") || calls != 1 || redirects != 0 {
					t.Fatalf("failure leaked, retried or redirected: calls=%d redirects=%d error=%v", calls, redirects, err)
				}
				// Constructor changes must stay on the copy, not alter the caller's client.
				if err := httpClient.CheckRedirect(nil, nil); err != nil || redirects != 1 {
					t.Fatal("constructor replaced the caller's redirect policy")
				}
			})
		}
	}
}

func TestKubernetesClientChecksStatusIdentityAndResponseBounds(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"unauthorized", 401, `{"message":"private-api-sentinel"}`, kubernetes.ErrForbidden},
		{"forbidden", 403, `{"message":"private-api-sentinel"}`, kubernetes.ErrForbidden},
		{"missing", 404, `{"message":"private-api-sentinel"}`, kubernetes.ErrNotFound},
		{"conflict", 409, `{"message":"private-api-sentinel"}`, kubernetes.ErrConflict},
		{"failed condition", 422, `{"message":"private-api-sentinel"}`, kubernetes.ErrConflict},
		{"expired", 410, `{"message":"private-api-sentinel"}`, kubernetes.ErrExpired},
		{"unavailable", 503, `{"message":"private-api-sentinel"}`, kubernetes.ErrUnavailable},
		{"wrong namespace", 200, strings.Replace(kubePodJSON, `"namespace":"agents"`, `"namespace":"other"`, 1), kubernetes.ErrInvalidResponse},
		{"wrong name", 200, strings.Replace(kubePodJSON, `fixture-agent-a1b2c3d4`, `other-agent-a1b2c3d4`, 1), kubernetes.ErrInvalidResponse},
		{"missing UID", 200, strings.Replace(kubePodJSON, `"opaque:pod/uid"`, `""`, 1), kubernetes.ErrInvalidResponse},
		{"missing version", 200, strings.Replace(kubePodJSON, `"opaque-rv:7/+"`, `""`, 1), kubernetes.ErrInvalidResponse},
		{"malformed", 200, `{"private-api-sentinel":`, kubernetes.ErrInvalidResponse},
		{"trailing JSON", 200, kubePodJSON + `{}`, kubernetes.ErrInvalidResponse},
		{"oversized", 200, strings.Repeat(" ", (1<<20)+1) + kubePodJSON, kubernetes.ErrInvalidResponse},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := &kubeTrackedBody{Reader: strings.NewReader(test.body)}
			client := kubeClient(t, func(request *http.Request) (*http.Response, error) {
				result := kubeResponse(request, test.status, "")
				result.Body = body
				return result, nil
			})
			_, err := client.Get(t.Context(), "fixture-agent-a1b2c3d4")
			if !errors.Is(err, test.want) || strings.Contains(err.Error(), "private-api-sentinel") {
				t.Fatalf("incorrect or unsafe error: %v; want %v", err, test.want)
			}
			if !body.closed.Load() || body.read.Load() > (1<<20)+1 {
				t.Fatal("response body was left open or read beyond its bound")
			}
		})
	}
}

func TestKubernetesClientSanitizesTransportAndBodyErrors(t *testing.T) {
	for _, operation := range []string{"create", "get", "release", "watch"} {
		for _, stage := range []string{"transport", "body"} {
			for _, cause := range []error{errors.New("private-api-sentinel"), context.Canceled, context.DeadlineExceeded} {
				t.Run(operation+"/"+stage+"/"+cause.Error(), func(t *testing.T) {
					failure := &url.Error{Op: "read", URL: "https://user:private-api-sentinel@other.invalid?token=private-api-sentinel", Err: fmt.Errorf("private-api-sentinel: %w", cause)}
					body := &kubeTrackedBody{Reader: kubeFailureReader{err: failure}}
					calls := 0
					client := kubeClient(t, func(request *http.Request) (*http.Response, error) {
						calls++
						if stage == "transport" {
							return nil, failure
						}
						status := http.StatusOK
						if operation == "create" {
							status = http.StatusCreated
						}
						result := kubeResponse(request, status, "")
						result.Body = body
						return result, nil
					})
					var err error
					switch operation {
					case "create":
						_, err = client.Create(t.Context(), kubeCreatePod(t))
					case "get":
						_, err = client.Get(t.Context(), "fixture-agent-a1b2c3d4")
					case "release":
						_, err = client.ReleaseGate(t.Context(), kubePod(t))
					case "watch":
						var watch *kubernetes.Watch
						watch, err = client.Watch(t.Context(), "fixture-agent-a1b2c3d4", "version")
						if err == nil {
							_, err = watch.Next()
							_ = watch.Close()
						}
					}
					if err == nil || strings.Contains(err.Error(), "private-api-sentinel") || strings.Contains(err.Error(), "other.invalid") || calls != 1 {
						t.Fatalf("API failure leaked its URL or retried: calls=%d error=%v", calls, err)
					}
					if (cause == context.Canceled || cause == context.DeadlineExceeded) && !errors.Is(err, cause) {
						t.Fatalf("API failure lost cancellation: %v; want %v", err, cause)
					}
					if stage == "body" && !body.closed.Load() {
						t.Fatal("failed API left its response body open")
					}
				})
			}
		}
	}
}

func TestKubernetesClientWatchesOnePodFromObservedVersion(t *testing.T) {
	pod := kubePod(t)
	events := `{"type":"ADDED","object":` + kubePodJSON + "}\n" +
		`{"type":"BOOKMARK","object":{"metadata":{"resourceVersion":"next-version"}}}` + "\n" +
		`{"type":"MODIFIED","object":` + strings.Replace(kubePodJSON, `"phase":"Pending"`, `"phase":"Running"`, 1) + "}\n" +
		`{"type":"DELETED","object":` + kubePodJSON + "}\n"
	body := &kubeTrackedBody{Reader: strings.NewReader(events)}
	calls := 0
	client := kubeClient(t, func(request *http.Request) (*http.Response, error) {
		calls++
		if request.Method != http.MethodGet || request.URL.Path != "/api/v1/namespaces/agents/pods" {
			t.Fatalf("unexpected watch target: %s %s", request.Method, request.URL)
		}
		query := request.URL.Query()
		timeout, err := strconv.Atoi(query.Get("timeoutSeconds"))
		if err != nil || timeout <= 0 || timeout > 300 {
			t.Fatal("watch has no bounded server timeout")
		}
		query.Del("timeoutSeconds")
		want := url.Values{"watch": {"true"}, "fieldSelector": {"metadata.name=" + pod.Metadata.Name},
			"resourceVersion": {pod.Metadata.ResourceVersion}, "allowWatchBookmarks": {"true"}}
		if !reflect.DeepEqual(query, want) {
			t.Fatalf("watch lost its name or opaque resource version: %v", query)
		}
		deadline, ok := request.Context().Deadline()
		if !ok || time.Until(deadline) > 301*time.Second {
			t.Fatal("watch has no bounded client timeout")
		}
		result := kubeResponse(request, http.StatusOK, "")
		result.Body = body
		return result, nil
	})
	watch, err := client.Watch(t.Context(), pod.Metadata.Name, pod.Metadata.ResourceVersion)
	if err != nil {
		t.Fatal(err)
	}
	defer watch.Close()
	for _, kind := range []kubernetes.EventType{kubernetes.Added, kubernetes.Bookmark, kubernetes.Modified, kubernetes.Deleted} {
		event, err := watch.Next()
		if err != nil || event.Type != kind {
			t.Fatalf("unexpected watch event: %+v, %v", event, err)
		}
		if kind == kubernetes.Bookmark {
			if event.Pod.Metadata.ResourceVersion != "next-version" {
				t.Fatal("watch lost the bookmark's resource version")
			}
		} else if event.Pod.Metadata.UID != pod.Metadata.UID || len(event.Pod.Spec) == 0 || len(event.Pod.Status) == 0 {
			t.Fatal("watch lost Pod identity or admitted state")
		}
	}
	if _, err := watch.Next(); !errors.Is(err, io.EOF) || calls != 1 {
		t.Fatalf("ended watch was reconnected or not reported as EOF: %v", err)
	}
	if !body.closed.Load() {
		t.Fatal("ended watch left its response body open")
	}
}

func TestKubernetesClientRejectsInvalidWatchEvents(t *testing.T) {
	for _, test := range []struct {
		name, body string
		status     int
		want       error
	}{
		{"expired HTTP", `{"message":"private-api-sentinel"}`, 410, kubernetes.ErrExpired},
		{"expired event", `{"type":"ERROR","object":{"kind":"Status","code":410,"message":"private-api-sentinel"}}`, 200, kubernetes.ErrExpired},
		{"forbidden event", `{"type":"ERROR","object":{"kind":"Status","code":403,"message":"private-api-sentinel"}}`, 200, kubernetes.ErrForbidden},
		{"unknown error", `{"type":"ERROR","object":{"message":"private-api-sentinel"}}`, 200, kubernetes.ErrInvalidResponse},
		{"unknown type", `{"type":"SURPRISE","object":` + kubePodJSON + `}`, 200, kubernetes.ErrInvalidResponse},
		{"wrong Pod", `{"type":"ADDED","object":` + strings.Replace(kubePodJSON, `fixture-agent-a1b2c3d4`, `other-agent-a1b2c3d4`, 1) + `}`, 200, kubernetes.ErrInvalidResponse},
		{"empty bookmark", `{"type":"BOOKMARK","object":{"metadata":{}}}`, 200, kubernetes.ErrInvalidResponse},
		{"malformed", `{"type":"ADDED","private-api-sentinel":`, 200, kubernetes.ErrInvalidResponse},
		{"oversized stream", strings.Repeat(" ", (8<<20)+64) + `{}`, 200, kubernetes.ErrInvalidResponse},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := &kubeTrackedBody{Reader: strings.NewReader(test.body)}
			calls := 0
			client := kubeClient(t, func(request *http.Request) (*http.Response, error) {
				calls++
				result := kubeResponse(request, test.status, "")
				result.Body = body
				return result, nil
			})
			watch, err := client.Watch(t.Context(), "fixture-agent-a1b2c3d4", "version")
			if err == nil {
				_, err = watch.Next()
				defer watch.Close()
			}
			if !errors.Is(err, test.want) || strings.Contains(err.Error(), "private-api-sentinel") || calls != 1 {
				t.Fatalf("watch error leaked, reconnected or had wrong kind: %v; want %v", err, test.want)
			}
			if !body.closed.Load() || body.read.Load() > (8<<20)+1 {
				t.Fatal("failed watch left its body open or read past its bound")
			}
		})
	}
}

func TestKubernetesClientStopsPendingWatch(t *testing.T) {
	for _, stop := range []string{"context", "watch", "client"} {
		t.Run(stop, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var body *kubeBlockingBody
			calls := 0
			client := kubeClient(t, func(request *http.Request) (*http.Response, error) {
				calls++
				body = &kubeBlockingBody{ctx: request.Context(), started: make(chan struct{}), closed: make(chan struct{})}
				result := kubeResponse(request, http.StatusOK, "")
				result.Body = body
				return result, nil
			})
			watch, err := client.Watch(ctx, "fixture-agent-a1b2c3d4", "version")
			if err != nil {
				t.Fatal(err)
			}
			defer watch.Close()
			finished := make(chan error, 1)
			go func() { _, err := watch.Next(); finished <- err }()
			select {
			case <-body.started:
			case <-time.After(time.Second):
				t.Fatal("watch did not start reading")
			}
			switch stop {
			case "context":
				cancel()
			case "watch":
				if err := watch.Close(); err != nil {
					t.Fatal(err)
				}
			case "client":
				if err := client.Close(); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case err := <-finished:
				if err == nil || stop == "context" && !errors.Is(err, context.Canceled) {
					t.Fatalf("watch lost cancellation or remained successful: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("stop did not interrupt the pending watch")
			}
			select {
			case <-body.closed:
			default:
				t.Fatal("stopped watch left its response body open")
			}
			if calls != 1 {
				t.Fatal("stopped watch reconnected")
			}
		})
	}
}

type kubeTrackedBody struct {
	io.Reader
	closed atomic.Bool
	read   atomic.Int64
}

func (body *kubeTrackedBody) Read(data []byte) (int, error) {
	n, err := body.Reader.Read(data)
	body.read.Add(int64(n))
	return n, err
}

func (body *kubeTrackedBody) Close() error { body.closed.Store(true); return nil }

type kubeFailureReader struct{ err error }

func (reader kubeFailureReader) Read([]byte) (int, error) { return 0, reader.err }

type kubeBlockingBody struct {
	ctx             context.Context
	started, closed chan struct{}
	readOnce        sync.Once
	closeOnce       sync.Once
}

func (body *kubeBlockingBody) Read([]byte) (int, error) {
	body.readOnce.Do(func() { close(body.started) })
	select {
	case <-body.ctx.Done():
		return 0, body.ctx.Err()
	case <-body.closed:
		return 0, io.ErrClosedPipe
	}
}

func (body *kubeBlockingBody) Close() error {
	body.closeOnce.Do(func() { close(body.closed) })
	return nil
}
