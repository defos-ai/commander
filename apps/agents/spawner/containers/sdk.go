package containers

import (
	"context"
	_ "crypto/sha256" // Register the digest algorithm used by image references.
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/containerd/errdefs"
	"github.com/distribution/reference"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

type dockerEngine struct{ client *client.Client }

// NewEngine reads operator-supplied Docker host/TLS settings. These settings are
// never forwarded to a child. The official client negotiates the API version.
func NewEngine() (Engine, error) {
	value, err := client.New(client.FromEnv)
	if err != nil {
		return nil, errors.New("configure Docker client: check Docker host and TLS settings")
	}
	return NewEngineWithClient(value)
}

// NewEngineWithClient takes ownership of an explicitly configured Docker client.
func NewEngineWithClient(value *client.Client) (Engine, error) {
	if value == nil {
		return nil, errors.New("Docker client is required")
	}
	return &dockerEngine{client: value}, nil
}

func (e *dockerEngine) Pull(ctx context.Context, image string) error {
	if !pinnedImage(image) {
		return errors.New("Docker image must be pinned to a SHA-256 digest")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	// Empty auth deliberately avoids forwarding the host's registry credentials.
	response, err := e.client.ImagePull(ctx, image, client.ImagePullOptions{})
	if err != nil {
		return dockerError(ctx, "pull Docker image", err)
	}
	defer response.Close()
	return dockerError(ctx, "pull Docker image", response.Wait(ctx))
}

func (e *dockerEngine) Inspect(ctx context.Context, nameOrID string) (Container, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if !validDockerName(nameOrID) {
		return Container{}, errors.New("invalid Docker container name or ID")
	}
	response, err := e.client.ContainerInspect(ctx, nameOrID, client.ContainerInspectOptions{})
	if err != nil {
		return Container{}, dockerError(ctx, "inspect Docker container", err)
	}
	value := response.Container
	result := Container{ID: value.ID, Name: strings.TrimPrefix(value.Name, "/")}
	if value.State != nil {
		result.Status = string(value.State.Status)
	}
	if value.Config != nil {
		result.Image = value.Config.Image
		result.Labels = maps.Clone(value.Config.Labels)
		result.Environment = make(map[string]string, len(value.Config.Env))
		for _, entry := range value.Config.Env {
			name, content, _ := strings.Cut(entry, "=")
			result.Environment[name] = content
		}
	}
	if value.NetworkSettings != nil {
		for name := range value.NetworkSettings.Networks {
			result.Networks = append(result.Networks, name)
		}
		slices.Sort(result.Networks)
	}
	result.Safe = safeContainer(value, result)
	return result, nil
}

func (e *dockerEngine) Create(ctx context.Context, spec Spec) (Container, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if !validDockerName(spec.Name) || !pinnedImage(spec.Image) || !explicitNetwork(spec.Network) {
		return Container{}, errors.New("Docker launch requires a name, digest-pinned image and explicit user-defined network")
	}
	environment := make([]string, 0, len(spec.Environment))
	for name, value := range spec.Environment {
		if name == "" || strings.ContainsAny(name, "=\x00") || strings.ContainsRune(value, '\x00') {
			return Container{}, errors.New("invalid Docker bootstrap environment")
		}
		environment = append(environment, name+"="+value)
	}
	slices.Sort(environment)
	inspected, err := e.client.NetworkInspect(ctx, spec.Network, client.NetworkInspectOptions{})
	if err != nil {
		return Container{}, dockerError(ctx, "inspect Docker network", err)
	}
	if inspected.Network.Name != spec.Network || inspected.Network.Driver == "" || inspected.Network.Driver == "host" || inspected.Network.Driver == "null" {
		return Container{}, errors.New("Docker network must be an existing user-defined network")
	}
	init := true
	response, err := e.client.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name: spec.Name,
		Config: &container.Config{
			Image: spec.Image, Env: environment, Labels: maps.Clone(spec.Labels),
		},
		HostConfig: &container.HostConfig{
			NetworkMode: container.NetworkMode(spec.Network),
			Init:        &init, CapDrop: []string{"ALL"}, SecurityOpt: []string{"no-new-privileges"},
			RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyDisabled},
			IpcMode:       container.IPCModePrivate, CgroupnsMode: container.CgroupnsModePrivate,
		},
		NetworkingConfig: &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{
			spec.Network: {},
		}},
	})
	if err != nil {
		return Container{}, dockerError(ctx, "create Docker container", err)
	}
	if response.ID == "" {
		return Container{}, errors.New("create Docker container: daemon returned no ID")
	}
	// Image defaults may add volumes or other settings. Inspect the actual
	// container before the manager decides whether it is safe to start.
	return e.Inspect(ctx, response.ID)
}

func (e *dockerEngine) Start(ctx context.Context, id string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if !validDockerName(id) {
		return errors.New("invalid Docker container ID")
	}
	_, err := e.client.ContainerStart(ctx, id, client.ContainerStartOptions{})
	return dockerError(ctx, "start Docker container", err)
}

func (e *dockerEngine) Close() error {
	return dockerError(context.Background(), "close Docker client", e.client.Close())
}

var dockerName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)
var sha256Digest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func validDockerName(value string) bool {
	return len(value) <= 128 && dockerName.MatchString(value)
}

func explicitNetwork(value string) bool {
	return validDockerName(value) && value != "host" && value != "none" && value != "bridge" && value != "default"
}

func pinnedImage(value string) bool {
	named, err := reference.ParseNormalizedNamed(value)
	if err != nil {
		return false
	}
	digest, ok := named.(reference.Digested)
	return ok && sha256Digest.MatchString(digest.Digest().String())
}

func safeContainer(value container.InspectResponse, result Container) bool {
	host := value.HostConfig
	if value.Platform != "linux" || host == nil || value.Config == nil || value.State == nil ||
		len(result.Networks) != 1 || !explicitNetwork(result.Networks[0]) || string(host.NetworkMode) != result.Networks[0] {
		return false
	}
	if host.Privileged || len(host.CapAdd) != 0 || !slices.Contains(host.CapDrop, "ALL") ||
		len(host.SecurityOpt) != 1 || (host.SecurityOpt[0] != "no-new-privileges" && host.SecurityOpt[0] != "no-new-privileges:true") {
		return false
	}
	if len(host.Binds) != 0 || len(host.Mounts) != 0 || len(host.VolumesFrom) != 0 || len(value.Mounts) != 0 ||
		len(host.PortBindings) != 0 || host.PublishAllPorts || len(host.Devices) != 0 || len(host.DeviceRequests) != 0 ||
		len(host.DeviceCgroupRules) != 0 || len(host.Links) != 0 || len(host.ExtraHosts) != 0 {
		return false
	}
	if host.PidMode != "" || host.IpcMode != container.IPCModePrivate || host.UTSMode != "" ||
		host.UsernsMode != "" || host.CgroupnsMode != container.CgroupnsModePrivate ||
		!host.RestartPolicy.IsNone() || host.AutoRemove || host.Init == nil || !*host.Init {
		return false
	}
	// Duplicate environment names have platform-dependent interpretations.
	return len(result.Environment) == len(value.Config.Env)
}

func dockerError(ctx context.Context, operation string, err error) error {
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return fmt.Errorf("%s: %w", operation, ctx.Err())
	}
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("%s: %w", operation, context.Canceled)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%s: %w", operation, context.DeadlineExceeded)
	}
	if errdefs.IsNotFound(err) {
		return fmt.Errorf("%s: %w", operation, ErrNotFound)
	}
	// Daemon errors and transport URLs can contain registry tokens or bootstrap
	// values. Keep them out of action results and logs.
	return fmt.Errorf("%s failed; check Docker daemon availability and configuration", operation)
}
