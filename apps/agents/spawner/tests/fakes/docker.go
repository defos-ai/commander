package fakes

import (
	"context"
	"errors"
	"fmt"
	"maps"

	"github.com/defos-ai/commander/apps/agents/spawner/runtimes/docker"
)

type DockerEngine struct {
	image     string
	current   map[string]docker.Container
	created   chan docker.Spec
	onStart   func(context.Context, docker.Container) error
	pullErr   error
	createErr error
	startErr  error
	pulls     int
	creates   int
	starts    int
	closes    int
}

func (e *DockerEngine) Pull(ctx context.Context, image string) error {
	e.pulls++
	if image != e.image {
		return errors.New("fixture received unexpected image")
	}
	if e.pullErr != nil {
		return e.pullErr
	}
	return ctx.Err()
}

func (e *DockerEngine) Inspect(ctx context.Context, name string) (docker.Container, error) {
	if err := ctx.Err(); err != nil {
		return docker.Container{}, err
	}
	for _, current := range e.current {
		if current.Name == name || current.ID == name {
			return CloneContainer(current), nil
		}
	}
	return docker.Container{}, docker.ErrNotFound
}

func (e *DockerEngine) Create(ctx context.Context, spec docker.Spec) (docker.Container, error) {
	if err := ctx.Err(); err != nil {
		return docker.Container{}, err
	}
	e.creates++
	if _, exists := e.current[spec.Name]; exists {
		return docker.Container{}, errors.New("fixture container name already exists")
	}
	current := docker.Container{
		ID: fmt.Sprintf("%064x", e.creates), Name: spec.Name, Image: spec.Image,
		Labels: maps.Clone(spec.Labels), Environment: maps.Clone(spec.Environment),
		Networks: []string{spec.Network}, Status: "created", Safe: true,
	}
	e.current[spec.Name] = current
	spec.Environment, spec.Labels = maps.Clone(spec.Environment), maps.Clone(spec.Labels)
	e.created <- spec
	err := e.createErr
	e.createErr = nil
	if err != nil {
		return docker.Container{}, err
	}
	return CloneContainer(current), nil
}

func (e *DockerEngine) Start(ctx context.Context, containerID string) error {
	var current docker.Container
	for name, candidate := range e.current {
		if candidate.ID == containerID {
			candidate.Status = "running"
			e.current[name], current = candidate, CloneContainer(candidate)
			break
		}
	}
	if current.ID == "" {
		return docker.ErrNotFound
	}
	e.starts++
	callback, err := e.onStart, e.startErr
	e.startErr = nil
	if callback != nil {
		if publishErr := callback(ctx, current); publishErr != nil {
			return publishErr
		}
	}
	return err
}

func (e *DockerEngine) Close() error {
	e.closes++
	return nil
}

func (e *DockerEngine) Change(change func(*docker.Container)) {
	for name, current := range e.current {
		current = CloneContainer(current)
		change(&current)
		e.current[name] = CloneContainer(current)
	}
}

func (e *DockerEngine) Counts() (creates, starts, closes int) {
	return e.creates, e.starts, e.closes
}

func CloneContainer(current docker.Container) docker.Container {
	current.Labels = maps.Clone(current.Labels)
	current.Environment = maps.Clone(current.Environment)
	current.Networks = append([]string(nil), current.Networks...)
	return current
}

func NewDockerEngine(image string) *DockerEngine {
	return &DockerEngine{image: image, current: make(map[string]docker.Container), created: make(chan docker.Spec, 10)}
}
func (e *DockerEngine) Created() <-chan docker.Spec { return e.created }
func (e *DockerEngine) SetOnStart(hook func(context.Context, docker.Container) error) {
	e.onStart = hook
}
func (e *DockerEngine) SetPullError(err error)   { e.pullErr = err }
func (e *DockerEngine) SetCreateError(err error) { e.createErr = err }
func (e *DockerEngine) SetStartError(err error)  { e.startErr = err }
func (e *DockerEngine) ForgetAll()               { clear(e.current) }
