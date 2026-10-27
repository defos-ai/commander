package runtimes

import (
	"github.com/defos-ai/commander/framework/domain/actions"
)

type RuntimeKind string

const (
	RuntimeNative     RuntimeKind = "native"
	RuntimeDocker     RuntimeKind = "docker"
	RuntimeKubernetes RuntimeKind = "kubernetes"
)

// RuntimeDetails carries the identity of the launched resource. Built-in runtimes
// use value records so copying a result does not share mutable identity data.
type RuntimeDetails interface {
	runtimeDetails()
}

type NativeRuntimeDetails struct {
	PID int `json:"pid"`
}

type DockerRuntimeDetails struct {
	ContainerID string `json:"container_id"`
}

type KubernetesRuntimeDetails struct {
	Namespace string `json:"namespace"`
	PodName   string `json:"pod_name"`
	PodUID    string `json:"pod_uid"`
}

func (NativeRuntimeDetails) runtimeDetails()     {}
func (DockerRuntimeDetails) runtimeDetails()     {}
func (KubernetesRuntimeDetails) runtimeDetails() {}

// Result reports an observed registration announcement. It does not promise
// controller persistence, readiness of every action, or future availability.
type Result struct {
	AgentID        string           `json:"agent_id"`
	AgentType      string           `json:"agent_type"`
	Package        string           `json:"package"`
	Runtime        RuntimeKind      `json:"runtime"`
	RuntimeDetails RuntimeDetails   `json:"runtime_details"`
	Actions        []actions.Action `json:"actions"`
}
