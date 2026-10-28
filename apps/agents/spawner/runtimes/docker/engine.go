package docker

import (
	"context"
	"errors"
)

var ErrNotFound = errors.New("container not found")

// ValidateNetwork rejects daemon modes and names that cannot identify an
// explicitly selected user-defined network. The daemon verifies it exists.
func ValidateNetwork(name string) error {
	if !explicitNetwork(name) {
		return errors.New("Docker spawner requires an explicit user-defined network")
	}
	return nil
}

// Container lifetime belongs to the deployment, so it has no stop/remove API.
type Engine interface {
	Pull(context.Context, string) error
	Inspect(context.Context, string) (Container, error)
	Create(context.Context, Spec) (Container, error)
	Start(context.Context, string) error
	Close() error
}

type Spec struct {
	Name        string
	Image       string
	Network     string
	Environment map[string]string
	Labels      map[string]string
}

type Container struct {
	ID          string
	Name        string
	Image       string
	Labels      map[string]string
	Environment map[string]string
	Networks    []string
	Status      string
	// Safe reports whether inspection confirms the spawner's constrained launch
	// configuration. It does not claim the image itself is trustworthy.
	Safe bool
}
