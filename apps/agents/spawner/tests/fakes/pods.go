package fakes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"sync"
	"sync/atomic"

	"github.com/defos-ai/commander/apps/agents/spawner/runtimes/kubernetes"
	"github.com/defos-ai/commander/framework/comms"
)

type PodClient struct {
	mu                      sync.Mutex
	current                 *kubernetes.Pod
	creates, releases, gets int
	closes                  int
	createErr, releaseErr   error
	beforeCreate            func(context.Context, kubernetes.Pod) error
	onCreate                func(context.Context, kubernetes.Pod) error
	beforeRelease           func(context.Context, kubernetes.Pod) error
	onRelease               func(context.Context, kubernetes.Pod) error
	onGet                   func(kubernetes.Pod)
	keepPending             bool
}

func (client *PodClient) Create(ctx context.Context, desired kubernetes.Pod) (kubernetes.Pod, error) {
	client.mu.Lock()
	before := client.beforeCreate
	client.mu.Unlock()
	if before != nil {
		if err := before(ctx, desired); err != nil {
			return kubernetes.Pod{}, err
		}
	}
	client.mu.Lock()
	client.creates++
	if client.current != nil {
		client.mu.Unlock()
		return kubernetes.Pod{}, kubernetes.ErrConflict
	}
	pod := ClonePod(desired)
	pod.Metadata.UID, pod.Metadata.ResourceVersion = "fixture-pod-uid", "1"
	pod.Status = json.RawMessage(`{"phase":"Pending"}`)
	client.current = &pod
	result, hook, failure := ClonePod(pod), client.onCreate, client.createErr
	client.mu.Unlock()
	if hook != nil {
		if err := hook(ctx, ClonePod(result)); err != nil {
			return kubernetes.Pod{}, err
		}
	}
	if failure != nil {
		return kubernetes.Pod{}, failure
	}
	return result, ctx.Err()
}

func (client *PodClient) Get(ctx context.Context, name string) (kubernetes.Pod, error) {
	client.mu.Lock()
	client.gets++
	if err := ctx.Err(); err != nil {
		client.mu.Unlock()
		return kubernetes.Pod{}, err
	}
	if client.current == nil || client.current.Metadata.Name != name {
		client.mu.Unlock()
		return kubernetes.Pod{}, kubernetes.ErrNotFound
	}
	pod, hook := ClonePod(*client.current), client.onGet
	client.mu.Unlock()
	if hook != nil {
		hook(ClonePod(pod))
	}
	return pod, nil
}

func (client *PodClient) ReleaseGate(ctx context.Context, observed kubernetes.Pod) (kubernetes.Pod, error) {
	client.mu.Lock()
	before := client.beforeRelease
	client.mu.Unlock()
	if before != nil {
		if err := before(ctx, observed); err != nil {
			return kubernetes.Pod{}, err
		}
	}
	client.mu.Lock()
	client.releases++
	if client.current == nil || client.current.Metadata.UID != observed.Metadata.UID || client.current.Metadata.ResourceVersion != observed.Metadata.ResourceVersion {
		client.mu.Unlock()
		return kubernetes.Pod{}, kubernetes.ErrConflict
	}
	var spec map[string]json.RawMessage
	if err := json.Unmarshal(client.current.Spec, &spec); err != nil {
		client.mu.Unlock()
		return kubernetes.Pod{}, err
	}
	var gates []map[string]string
	if err := json.Unmarshal(spec["schedulingGates"], &gates); err != nil {
		client.mu.Unlock()
		return kubernetes.Pod{}, err
	}
	var remaining []map[string]string
	found := false
	for _, gate := range gates {
		if gate["name"] == kubernetes.SchedulingGate {
			found = true
		} else {
			remaining = append(remaining, gate)
		}
	}
	if !found {
		client.mu.Unlock()
		return kubernetes.Pod{}, errors.New("fixture refused to release an absent gate")
	}
	if len(remaining) == 0 {
		delete(spec, "schedulingGates")
	} else {
		spec["schedulingGates"], _ = json.Marshal(remaining)
	}
	client.current.Spec, _ = json.Marshal(spec)
	client.current.Metadata.ResourceVersion = "2"
	if !client.keepPending {
		SetPodRunning(client.current)
	}
	pod, hook, failure := ClonePod(*client.current), client.onRelease, client.releaseErr
	client.mu.Unlock()
	if hook != nil {
		if err := hook(ctx, ClonePod(pod)); err != nil {
			return kubernetes.Pod{}, err
		}
	}
	if failure != nil {
		return kubernetes.Pod{}, failure
	}
	return pod, ctx.Err()
}

// The manager accepts a narrower interface and must leave this caller-owned
// client untouched, even though the fixture also happens to implement Close.
func (client *PodClient) Close() error {
	client.mu.Lock()
	defer client.mu.Unlock()
	client.closes++
	return nil
}

func (client *PodClient) Counts() (creates, releases, gets, closes int) {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.creates, client.releases, client.gets, client.closes
}

func (client *PodClient) Snapshot() kubernetes.Pod {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.current == nil {
		return kubernetes.Pod{}
	}
	return ClonePod(*client.current)
}

func (client *PodClient) Change(edit func(*kubernetes.Pod)) {
	client.mu.Lock()
	defer client.mu.Unlock()
	pod := ClonePod(*client.current)
	edit(&pod)
	cloned := ClonePod(pod)
	client.current = &cloned
}

func ClonePod(pod kubernetes.Pod) kubernetes.Pod {
	pod.Spec, pod.Status = bytes.Clone(pod.Spec), bytes.Clone(pod.Status)
	pod.Metadata.Labels, pod.Metadata.Annotations = maps.Clone(pod.Metadata.Labels), maps.Clone(pod.Metadata.Annotations)
	pod.Metadata.Finalizers = append([]string(nil), pod.Metadata.Finalizers...)
	pod.Metadata.OwnerReferences = append([]json.RawMessage(nil), pod.Metadata.OwnerReferences...)
	for i := range pod.Metadata.OwnerReferences {
		pod.Metadata.OwnerReferences[i] = bytes.Clone(pod.Metadata.OwnerReferences[i])
	}
	if pod.Metadata.DeletionTimestamp != nil {
		value := *pod.Metadata.DeletionTimestamp
		pod.Metadata.DeletionTimestamp = &value
	}
	return pod
}

func SetPodRunning(pod *kubernetes.Pod) {
	pod.Status = json.RawMessage(`{"phase":"Running","containerStatuses":[{"name":"agent","containerID":"containerd://fixture-1","restartCount":0,"ready":true,"started":true,"state":{"running":{"startedAt":"2026-10-27T10:00:00Z"}}}]}`)
}

type TrackingBus struct {
	comms.Bus
	subscriptions atomic.Int64
}

func (bus *TrackingBus) Subscribe(ctx context.Context, inbox comms.Inbox) (comms.Subscription, error) {
	subscription, err := bus.Bus.Subscribe(ctx, inbox)
	if err == nil {
		bus.subscriptions.Add(1)
	}
	return subscription, err
}

func (client *PodClient) SetCreateError(err error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	client.createErr = err
}
func (client *PodClient) SetReleaseError(err error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	client.releaseErr = err
}
func (client *PodClient) SetBeforeCreate(hook func(context.Context, kubernetes.Pod) error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	client.beforeCreate = hook
}
func (client *PodClient) SetOnCreate(hook func(context.Context, kubernetes.Pod) error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	client.onCreate = hook
}
func (client *PodClient) SetBeforeRelease(hook func(context.Context, kubernetes.Pod) error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	client.beforeRelease = hook
}
func (client *PodClient) SetOnRelease(hook func(context.Context, kubernetes.Pod) error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	client.onRelease = hook
}
func (client *PodClient) SetOnGet(hook func(kubernetes.Pod)) {
	client.mu.Lock()
	defer client.mu.Unlock()
	client.onGet = hook
}
func (client *PodClient) SetKeepPending(keep bool) {
	client.mu.Lock()
	defer client.mu.Unlock()
	client.keepPending = keep
}
func (client *PodClient) Forget()                 { client.mu.Lock(); defer client.mu.Unlock(); client.current = nil }
func (bus *TrackingBus) SubscriptionCount() int64 { return bus.subscriptions.Load() }
