package runtimes

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// UnmarshalJSON restores the concrete details selected by Runtime. The action's
// output schema validates identity values such as PID bounds and Pod names.
func (r *Result) UnmarshalJSON(data []byte) error {
	type plain Result
	var result plain
	wire := struct {
		*plain
		RuntimeDetails json.RawMessage `json:"runtime_details"`
	}{plain: &result}
	if err := decodeObject(data, &wire); err != nil {
		return err
	}
	details, err := decodeRuntimeDetails(result.Runtime, wire.RuntimeDetails)
	if err != nil {
		return err
	}
	result.RuntimeDetails = details
	*r = Result(result)
	return nil
}

func decodeRuntimeDetails(kind RuntimeKind, data []byte) (RuntimeDetails, error) {
	switch kind {
	case RuntimeNative:
		var details NativeRuntimeDetails
		err := decodeObject(data, &details)
		return details, err
	case RuntimeDocker:
		var details DockerRuntimeDetails
		err := decodeObject(data, &details)
		return details, err
	case RuntimeKubernetes:
		var details KubernetesRuntimeDetails
		err := decodeObject(data, &details)
		return details, err
	default:
		return nil, errors.New("unsupported spawn result runtime")
	}
}

func decodeObject(data []byte, target any) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || data[0] != '{' {
		return errors.New("spawn result and runtime details must be JSON objects")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("expected one JSON object")
	}
	return nil
}
