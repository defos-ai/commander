package kubernetes

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type PodState struct {
	Gated       bool
	Phase       string
	ContainerID string
	StartedAt   string
}

// InspectPod compares an admitted Pod with the immutable BuildPod intent. It
// permits only known API defaults; new spec fields require explicit review.
func InspectPod(expected, actual Pod) (PodState, error) {
	invalid := errors.New("Kubernetes Pod identity or admitted configuration differs from launch intent")
	if expected.APIVersion != "v1" || expected.Kind != "Pod" || actual.APIVersion != expected.APIVersion || actual.Kind != expected.Kind ||
		actual.Metadata.Name != expected.Metadata.Name || actual.Metadata.Namespace != expected.Metadata.Namespace ||
		actual.Metadata.UID == "" || actual.Metadata.ResourceVersion == "" ||
		(expected.Metadata.UID != "" && expected.Metadata.UID != actual.Metadata.UID) ||
		actual.Metadata.DeletionTimestamp != nil || len(actual.Metadata.OwnerReferences) != 0 || len(actual.Metadata.Finalizers) != 0 ||
		!containsPodMetadata(expected.Metadata.Labels, actual.Metadata.Labels) || !containsPodMetadata(expected.Metadata.Annotations, actual.Metadata.Annotations) {
		return PodState{}, invalid
	}
	want, err := inspectObject(expected.Spec)
	if err != nil {
		return PodState{}, invalid
	}
	got, err := inspectObject(actual.Spec)
	if err != nil {
		return PodState{}, invalid
	}
	gated, err := inspectPodSpec(want, got)
	if err != nil {
		return PodState{}, invalid
	}
	state, err := inspectPodStatus(actual.Status, gated)
	if err != nil {
		return PodState{}, err
	}
	return state, nil
}

func containsPodMetadata(want, got map[string]string) bool {
	for key, value := range want {
		if actual, present := got[key]; !present || actual != value {
			return false
		}
	}
	return true
}

func inspectPodSpec(want, got map[string]any) (bool, error) {
	invalid := errors.New("invalid admitted Pod specification")
	gated, err := inspectSchedulingGates(got["schedulingGates"])
	if err != nil {
		return false, invalid
	}
	// Preserve this check before removing scheduling-only fields for comparison.
	if node, present := got["nodeName"]; present {
		name, ok := node.(string)
		if !ok || (name != "" && (!validPodDNSName(name) || gated)) {
			return false, invalid
		}
		delete(got, "nodeName")
	}
	delete(want, "schedulingGates")
	delete(got, "schedulingGates")
	for key, value := range map[string]any{
		"dnsPolicy": "ClusterFirst", "schedulerName": "default-scheduler",
		"terminationGracePeriodSeconds": json.Number("30"), "preemptionPolicy": "PreemptLowerPriority", "priority": json.Number("0"),
		"hostNetwork": false, "hostPID": false, "hostIPC": false, "shareProcessNamespace": false,
	} {
		if !removePodDefault(got, key, value) {
			return false, invalid
		}
	}
	if !removePodDefault(got, "serviceAccount", want["serviceAccountName"]) || !inspectPodTolerations(got) {
		return false, invalid
	}
	wantContainers, wantOK := want["containers"].([]any)
	gotContainers, gotOK := got["containers"].([]any)
	if !wantOK || !gotOK || len(wantContainers) != 1 || len(gotContainers) != 1 {
		return false, invalid
	}
	wantContainer, wantOK := wantContainers[0].(map[string]any)
	gotContainer, gotOK := gotContainers[0].(map[string]any)
	if !wantOK || !gotOK {
		return false, invalid
	}
	for key, value := range map[string]any{
		"terminationMessagePath": "/dev/termination-log", "terminationMessagePolicy": "File",
		"stdin": false, "stdinOnce": false, "tty": false,
	} {
		if !removePodDefault(gotContainer, key, value) {
			return false, invalid
		}
	}
	if !inspectPodResources(wantContainer["resources"], gotContainer["resources"]) || !reflect.DeepEqual(want, got) {
		return false, invalid
	}
	return gated, nil
}

func removePodDefault(object map[string]any, key string, value any) bool {
	if actual, present := object[key]; present {
		if !reflect.DeepEqual(actual, value) {
			return false
		}
		delete(object, key)
	}
	return true
}

func inspectSchedulingGates(value any) (bool, error) {
	if value == nil {
		return false, nil
	}
	gates, ok := value.([]any)
	if !ok {
		return false, errors.New("invalid scheduling gates")
	}
	seen := map[string]bool{}
	for _, gate := range gates {
		entry, ok := gate.(map[string]any)
		name, valid := entry["name"].(string)
		if !ok || !valid || name == "" || len(entry) != 1 || seen[name] {
			return false, errors.New("invalid scheduling gates")
		}
		seen[name] = true
	}
	return seen[SchedulingGate], nil
}

func inspectPodTolerations(spec map[string]any) bool {
	value, present := spec["tolerations"]
	if !present {
		return true
	}
	entries, ok := value.([]any)
	if !ok || len(entries) > 2 {
		return false
	}
	seen := map[string]bool{}
	for _, value := range entries {
		entry, ok := value.(map[string]any)
		key, _ := entry["key"].(string)
		if !ok || seen[key] || (key != "node.kubernetes.io/not-ready" && key != "node.kubernetes.io/unreachable") {
			return false
		}
		seen[key] = true
		if !removePodDefault(entry, "value", "") || !reflect.DeepEqual(entry, map[string]any{
			"key": key, "operator": "Exists", "effect": "NoExecute", "tolerationSeconds": json.Number("300"),
		}) {
			return false
		}
	}
	delete(spec, "tolerations")
	return true
}

func inspectPodResources(expected, actual any) bool {
	want, wantOK := expected.(map[string]any)
	got, gotOK := actual.(map[string]any)
	if !wantOK || !gotOK || len(want) != len(got) {
		return false
	}
	for kind, value := range want {
		wantValues, wantOK := value.(map[string]any)
		gotValues, gotOK := got[kind].(map[string]any)
		if !wantOK || !gotOK || len(wantValues) != len(gotValues) {
			return false
		}
		for key, value := range wantValues {
			wanted, wantOK := value.(string)
			actual, gotOK := gotValues[key].(string)
			left, leftOK := podQuantity(wanted)
			right, rightOK := podQuantity(actual)
			if !wantOK || !gotOK || !leftOK || !rightOK || left.Cmp(right) != 0 {
				return false
			}
			gotValues[key] = wanted
		}
	}
	return true
}

// Quantity canonicalization changes spelling, not mathematical value. Compare
// exact rationals; do not accept API rounding/capping as an operator override.
var podQuantityPattern = regexp.MustCompile(`^([+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+))([numkMGTPE]|[KMGTPE]i|[eE][+-]?[0-9]+)?$`)

func podQuantity(value string) (*big.Rat, bool) {
	if len(value) == 0 || len(value) > 256 {
		return nil, false
	}
	parts := podQuantityPattern.FindStringSubmatch(value)
	if parts == nil {
		return nil, false
	}
	number, ok := new(big.Rat).SetString(parts[1])
	if !ok {
		return nil, false
	}
	suffix := parts[2]
	exponent, base := 0, int64(10)
	if strings.HasSuffix(suffix, "i") {
		exponent, base = 10*(strings.Index("KMGTPE", suffix[:1])+1), 2
	} else if len(suffix) > 1 {
		parsed, err := strconv.Atoi(suffix[1:])
		if err != nil || parsed < -1000 || parsed > 1000 {
			return nil, false
		}
		exponent = parsed
	} else {
		exponent = map[string]int{"n": -9, "u": -6, "m": -3, "k": 3, "M": 6, "G": 9, "T": 12, "P": 15, "E": 18}[suffix]
	}
	power := new(big.Int).Exp(big.NewInt(base), big.NewInt(int64(absPodExponent(exponent))), nil)
	factor := new(big.Rat).SetInt(power)
	if exponent < 0 {
		number.Quo(number, factor)
	} else {
		number.Mul(number, factor)
	}
	return number, true
}

func absPodExponent(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func inspectPodStatus(raw json.RawMessage, gated bool) (PodState, error) {
	invalid := errors.New("Kubernetes Pod execution state is invalid or no longer owns its original instance")
	status, err := inspectObject(raw)
	if err != nil || (status["phase"] != "Pending" && status["phase"] != "Running") {
		return PodState{}, invalid
	}
	state := PodState{Gated: gated, Phase: status["phase"].(string)}
	for _, key := range []string{"initContainerStatuses", "ephemeralContainerStatuses"} {
		if value, present := status[key]; present {
			entries, ok := value.([]any)
			if !ok || len(entries) != 0 {
				return PodState{}, invalid
			}
		}
	}
	value, present := status["containerStatuses"]
	if !present {
		if state.Phase == "Running" {
			return PodState{}, invalid
		}
		return state, nil
	}
	entries, ok := value.([]any)
	if !ok || len(entries) != 1 {
		return PodState{}, invalid
	}
	container, ok := entries[0].(map[string]any)
	if !ok || container["name"] != "agent" || container["restartCount"] != json.Number("0") {
		return PodState{}, invalid
	}
	if previous, present := container["lastState"]; present {
		prior, ok := previous.(map[string]any)
		if !ok || len(prior) != 0 {
			return PodState{}, invalid
		}
	}
	current, ok := container["state"].(map[string]any)
	if !ok || len(current) > 1 {
		return PodState{}, invalid
	}
	if _, terminated := current["terminated"]; terminated {
		return PodState{}, invalid
	}
	if running, present := current["running"]; present {
		value, ok := running.(map[string]any)
		state.ContainerID, _ = container["containerID"].(string)
		state.StartedAt, _ = value["startedAt"].(string)
		if !ok || gated || state.Phase != "Running" || state.ContainerID == "" || state.StartedAt == "" {
			return PodState{}, invalid
		}
		if _, err := time.Parse(time.RFC3339Nano, state.StartedAt); err != nil {
			return PodState{}, invalid
		}
		return state, nil
	}
	if value, present := current["waiting"]; present {
		if _, ok := value.(map[string]any); !ok {
			return PodState{}, invalid
		}
	} else if len(current) != 0 {
		return PodState{}, invalid
	}
	if state.Phase != "Pending" || (gated && (container["containerID"] != nil && container["containerID"] != "" || container["started"] == true || container["ready"] == true)) {
		return PodState{}, invalid
	}
	return state, nil
}

// Reject duplicate keys and excessive nesting before projecting security fields.
func inspectObject(raw []byte) (map[string]any, error) {
	if len(raw) == 0 || len(raw) > maxPodBytes {
		return nil, errors.New("invalid Pod JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	value, err := inspectJSONValue(decoder, 0)
	object, ok := value.(map[string]any)
	if err != nil || !ok {
		return nil, errors.New("invalid Pod JSON object")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, errors.New("invalid Pod JSON document")
	}
	return object, nil
}

func inspectJSONValue(decoder *json.Decoder, depth int) (any, error) {
	if depth > 64 {
		return nil, errors.New("invalid Pod JSON depth")
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	switch token {
	case json.Delim('{'):
		object := map[string]any{}
		for decoder.More() {
			key, err := decoder.Token()
			name, ok := key.(string)
			if err != nil || !ok {
				return nil, errors.New("invalid Pod JSON key")
			}
			if _, exists := object[name]; exists {
				return nil, errors.New("duplicate Pod JSON key")
			}
			value, err := inspectJSONValue(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			object[name] = value
		}
		_, err := decoder.Token()
		return object, err
	case json.Delim('['):
		var values []any
		for decoder.More() {
			value, err := inspectJSONValue(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		}
		_, err := decoder.Token()
		return values, err
	default:
		return token, nil
	}
}
