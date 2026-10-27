package kubernetes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

var (
	ErrNotFound        = errors.New("Kubernetes Pod not found")
	ErrConflict        = errors.New("Kubernetes Pod conflict or failed precondition")
	ErrForbidden       = errors.New("Kubernetes API access denied")
	ErrExpired         = errors.New("Kubernetes watch history expired")
	ErrUnavailable     = errors.New("Kubernetes API request failed")
	ErrInvalidResponse = errors.New("invalid Kubernetes API response")
)

const (
	requestTimeout = 30 * time.Second
	watchTimeout   = 5 * time.Minute
	maxPodBytes    = 1 << 20
	maxWatchBytes  = 8 << 20
)

// Client exposes only the namespace-scoped Pod operations needed by a future
// launcher. It never retries writes, reconnects watches, or deletes Pods.
type Client struct {
	namespace      string
	server         string
	http           http.Client
	ctx            context.Context
	cancel         context.CancelFunc
	closeTransport func()
	closeOnce      sync.Once
}

// NewClient uses an explicitly configured HTTPS server and HTTP transport.
// Authentication belongs to that transport; no kubeconfig, credentials or proxy
// settings are discovered here. The transport remains caller-owned and must not
// replay mutations. Redirects are disabled on a private copy of the HTTP client.
func NewClient(namespace, server string, client *http.Client) (*Client, error) {
	endpoint, err := url.Parse(server)
	if !clientNamespace(namespace) || err != nil || endpoint.Scheme != "https" || endpoint.Host == "" ||
		endpoint.User != nil || endpoint.Opaque != "" || endpoint.RawQuery != "" || endpoint.ForceQuery ||
		endpoint.Fragment != "" || endpoint.RawFragment != "" || (endpoint.Path != "" && endpoint.Path != "/") {
		return nil, errors.New("Kubernetes client requires a namespace and an HTTPS API server without credentials or a path")
	}
	if client == nil || client.Transport == nil {
		return nil, errors.New("Kubernetes client requires an explicitly configured HTTP transport")
	}
	httpClient := *client
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	ctx, cancel := context.WithCancel(context.Background())
	return &Client{namespace: namespace, server: strings.TrimSuffix(endpoint.String(), "/"), http: httpClient, ctx: ctx, cancel: cancel}, nil
}

// Create submits a new scheduling-gated Pod once. An error after submission can
// mean the Pod was created; only reconciliation may resolve that uncertainty.
func (c *Client) Create(ctx context.Context, pod Pod) (Pod, error) {
	if err := c.identity(pod, false); err != nil {
		return Pod{}, err
	}
	var spec struct {
		RestartPolicy string `json:"restartPolicy"`
	}
	if json.Unmarshal(pod.Spec, &spec) != nil || spec.RestartPolicy != "Never" {
		return Pod{}, errors.New("Kubernetes Pod creation requires restartPolicy Never")
	}
	if _, err := gateIndex(pod.Spec); err != nil {
		return Pod{}, err
	}
	body, err := json.Marshal(pod)
	if err != nil || len(body) > maxPodBytes {
		return Pod{}, errors.New("invalid Kubernetes Pod creation request")
	}
	return c.podRequest(ctx, http.MethodPost, "", "application/json", body, http.StatusCreated, pod.Metadata.Name, "")
}

func (c *Client) Get(ctx context.Context, name string) (Pod, error) {
	if !clientPodName(name) {
		return Pod{}, errors.New("invalid Kubernetes Pod name")
	}
	return c.podRequest(ctx, http.MethodGet, name, "", nil, http.StatusOK, name, "")
}

// ReleaseGate addresses the observed instance and version. Only the owned gate
// is removed; gates added by admission remain intact. Success does not certify
// registration or authorize launching without the future Manager's checkpoints.
func (c *Client) ReleaseGate(ctx context.Context, observed Pod) (Pod, error) {
	if err := c.identity(observed, true); err != nil {
		return Pod{}, err
	}
	index, err := gateIndex(observed.Spec)
	if err != nil {
		return Pod{}, err
	}
	path := fmt.Sprintf("/spec/schedulingGates/%d", index)
	patch := []map[string]any{
		{"op": "test", "path": "/metadata/uid", "value": observed.Metadata.UID},
		{"op": "test", "path": "/metadata/resourceVersion", "value": observed.Metadata.ResourceVersion},
		{"op": "test", "path": path + "/name", "value": SchedulingGate},
		{"op": "remove", "path": path},
	}
	body, _ := json.Marshal(patch)
	return c.podRequest(ctx, http.MethodPatch, observed.Metadata.Name, "application/json-patch+json", body, http.StatusOK, observed.Metadata.Name, observed.Metadata.UID)
}

// Close cancels requests and watches without changing any Pod. In-cluster
// clients close their own idle connections; injected transports remain owned by
// the caller. It is safe to call more than once.
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		c.cancel()
		if c.closeTransport != nil {
			c.closeTransport()
		}
	})
	return nil
}

func (c *Client) podRequest(ctx context.Context, method, name, contentType string, body []byte, status int, expectedName, expectedUID string) (Pod, error) {
	ctx, done := c.requestContext(ctx, requestTimeout)
	defer done()
	response, err := c.request(ctx, method, name, nil, contentType, body)
	if err != nil {
		return Pod{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != status {
		return Pod{}, statusError(response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxPodBytes+1))
	if err != nil {
		return Pod{}, requestError(ctx, err)
	}
	var pod Pod
	if len(data) > maxPodBytes || json.Unmarshal(data, &pod) != nil ||
		c.identity(pod, true) != nil || pod.Metadata.Name != expectedName ||
		(expectedUID != "" && pod.Metadata.UID != expectedUID) || !jsonObject(pod.Spec) {
		return Pod{}, ErrInvalidResponse
	}
	return pod, nil
}

func (c *Client) request(ctx context.Context, method, name string, query url.Values, contentType string, body []byte) (*http.Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	endpoint := c.server + "/api/v1/namespaces/" + url.PathEscape(c.namespace) + "/pods"
	if name != "" {
		endpoint += "/" + url.PathEscape(name)
	}
	if len(query) != 0 {
		endpoint += "?" + query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, ErrUnavailable
	}
	// Do not provide a replayable body or idempotency headers for mutations.
	request.GetBody = nil
	request.Header.Set("Accept", "application/json")
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return nil, requestError(ctx, err)
	}
	return response, nil
}

func (c *Client) requestContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	stop := context.AfterFunc(c.ctx, cancel)
	if c.ctx.Err() != nil {
		cancel()
	}
	return ctx, func() { stop(); cancel() }
}

func (c *Client) identity(pod Pod, observed bool) error {
	if pod.APIVersion != "v1" || pod.Kind != "Pod" || pod.Metadata.Namespace != c.namespace || !clientPodName(pod.Metadata.Name) {
		return errors.New("invalid Kubernetes Pod identity")
	}
	if observed {
		if !opaqueVersion(pod.Metadata.UID) || !opaqueVersion(pod.Metadata.ResourceVersion) {
			return errors.New("Kubernetes Pod UID and resource version are required")
		}
	} else if pod.Metadata.UID != "" || pod.Metadata.ResourceVersion != "" || pod.Metadata.DeletionTimestamp != nil {
		return errors.New("Kubernetes Pod creation cannot reuse a server-assigned identity")
	}
	return nil
}

func gateIndex(spec json.RawMessage) (int, error) {
	var value struct {
		Gates []struct {
			Name string `json:"name"`
		} `json:"schedulingGates"`
	}
	if !jsonObject(spec) || json.Unmarshal(spec, &value) != nil {
		return 0, errors.New("invalid Kubernetes Pod specification")
	}
	index := -1
	for i, gate := range value.Gates {
		if gate.Name == SchedulingGate {
			if index != -1 {
				return 0, errors.New("duplicate Kubernetes launch gate")
			}
			index = i
		}
	}
	if index == -1 {
		return 0, errors.New("Kubernetes launch gate is required")
	}
	return index, nil
}

func jsonObject(value json.RawMessage) bool {
	value = bytes.TrimSpace(value)
	return len(value) > 1 && value[0] == '{' && json.Valid(value)
}

func clientNamespace(value string) bool {
	return len(value) <= 63 && podDNSLabelPattern.MatchString(value)
}

func clientPodName(value string) bool {
	return validPodDNSName(value)
}

func opaqueVersion(value string) bool {
	return len(value) > 0 && len(value) <= 1024 && !strings.ContainsFunc(value, func(r rune) bool { return r <= 32 || r == 127 })
}

func statusError(status int) error {
	var cause error
	switch status {
	case http.StatusNotFound:
		cause = ErrNotFound
	case http.StatusConflict, http.StatusUnprocessableEntity:
		cause = ErrConflict
	case http.StatusUnauthorized, http.StatusForbidden:
		cause = ErrForbidden
	case http.StatusGone:
		cause = ErrExpired
	default:
		cause = ErrUnavailable
	}
	// Server bodies and transport errors can contain credentials. Retain only
	// the HTTP code and a stable classification for reconciliation.
	return fmt.Errorf("%w (HTTP %d)", cause, status)
}

func requestError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return ErrUnavailable
}

type EventType string

const (
	Added    EventType = "ADDED"
	Modified EventType = "MODIFIED"
	Deleted  EventType = "DELETED"
	Bookmark EventType = "BOOKMARK"
)

type Event struct {
	Type EventType
	Pod  Pod
}

// Watch is one bounded stream. Call Next from one goroutine; Close may run
// concurrently. EOF or an expired resource version requires reconciliation by
// the caller, never an automatic reconnect or another Create.
type Watch struct {
	client  *Client
	name    string
	ctx     context.Context
	cancel  context.CancelFunc
	body    io.ReadCloser
	limit   *io.LimitedReader
	decoder *json.Decoder
	stop    func() bool
	once    sync.Once
}

type watchBody struct {
	io.ReadCloser
	once sync.Once
}

func (b *watchBody) Close() error {
	b.once.Do(func() { b.ReadCloser.Close() })
	return nil
}

func (c *Client) Watch(ctx context.Context, name, resourceVersion string) (*Watch, error) {
	if !clientPodName(name) || !opaqueVersion(resourceVersion) {
		return nil, errors.New("Kubernetes watch requires a Pod name and resource version")
	}
	ctx, cancel := c.requestContext(ctx, watchTimeout)
	query := url.Values{
		"watch": {"true"}, "fieldSelector": {"metadata.name=" + name},
		"resourceVersion": {resourceVersion}, "allowWatchBookmarks": {"true"}, "timeoutSeconds": {"300"},
	}
	response, err := c.request(ctx, http.MethodGet, "", query, "", nil)
	if err != nil {
		cancel()
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		cancel()
		return nil, statusError(response.StatusCode)
	}
	body := &watchBody{ReadCloser: response.Body}
	limit := &io.LimitedReader{R: body, N: maxWatchBytes + 1}
	watch := &Watch{client: c, name: name, ctx: ctx, cancel: cancel, body: body, limit: limit, decoder: json.NewDecoder(limit)}
	watch.stop = context.AfterFunc(ctx, func() { body.Close() })
	return watch, nil
}

func (w *Watch) Next() (Event, error) {
	if err := w.ctx.Err(); err != nil {
		w.Close()
		return Event{}, err
	}
	var message struct {
		Type   EventType       `json:"type"`
		Object json.RawMessage `json:"object"`
	}
	err := w.decoder.Decode(&message)
	if err != nil || w.limit.N == 0 {
		result := ErrInvalidResponse
		if w.ctx.Err() != nil {
			result = w.ctx.Err()
		} else if errors.Is(err, context.Canceled) {
			result = context.Canceled
		} else if errors.Is(err, context.DeadlineExceeded) {
			result = context.DeadlineExceeded
		} else if err == io.EOF && w.limit.N > 0 {
			result = io.EOF
		}
		w.Close()
		return Event{}, result
	}
	if message.Type == "ERROR" {
		var status struct {
			Code int `json:"code"`
		}
		err := ErrInvalidResponse
		if json.Unmarshal(message.Object, &status) == nil && status.Code >= 400 && status.Code <= 599 {
			err = statusError(status.Code)
		}
		w.Close()
		return Event{}, err
	}
	var pod Pod
	valid := json.Unmarshal(message.Object, &pod) == nil
	switch message.Type {
	case Bookmark:
		valid = valid && opaqueVersion(pod.Metadata.ResourceVersion)
	case Added, Modified, Deleted:
		valid = valid && w.client.identity(pod, true) == nil && pod.Metadata.Name == w.name && jsonObject(pod.Spec)
	default:
		valid = false
	}
	if !valid {
		w.Close()
		return Event{}, ErrInvalidResponse
	}
	return Event{Type: message.Type, Pod: pod}, nil
}

func (w *Watch) Close() error {
	w.once.Do(func() {
		w.stop()
		w.cancel()
		w.body.Close()
	})
	return nil
}
