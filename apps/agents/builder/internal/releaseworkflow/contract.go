// Package releaseworkflow checks the template contract used for safe release dispatch.
package releaseworkflow

import (
	"bytes"
	"errors"
	"io"
	"strings"

	"go.yaml.in/yaml/v3"
)

const runName = "Release agent ${{ inputs.version }} ${{ inputs.request_id }}"

const commitGuard = `if [ -n "$EXPECTED_COMMIT" ]; then
  test "$GITHUB_SHA" = "$EXPECTED_COMMIT"
fi`

type workflow struct {
	RunName string `yaml:"run-name"`
	On      map[string]struct {
		Inputs map[string]struct {
			Type     string `yaml:"type"`
			Required bool   `yaml:"required"`
		} `yaml:"inputs"`
	} `yaml:"on"`
	Jobs map[string]job `yaml:"jobs"`
}

type job struct {
	Needs           yaml.Node `yaml:"needs"`
	If              string    `yaml:"if"`
	ContinueOnError yaml.Node `yaml:"continue-on-error"`
	Steps           []step    `yaml:"steps"`
}

type step struct {
	Name            string            `yaml:"name"`
	Uses            string            `yaml:"uses"`
	Run             string            `yaml:"run"`
	If              string            `yaml:"if"`
	ContinueOnError yaml.Node         `yaml:"continue-on-error"`
	Env             map[string]string `yaml:"env"`
	With            map[string]string `yaml:"with"`
}

// Validate requires the known release template's dispatch and commit guard.
// It is a structural check, not a sandbox for arbitrary workflow code.
func Validate(data []byte) error {
	invalid := errors.New("make-release.yml must preserve the release dispatch inputs, request run-name, initial commit guard and prepare dependencies")
	if len(data) == 0 || len(data) > 1<<20 {
		return invalid
	}
	var value workflow
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	if decoder.Decode(&value) != nil || decoder.Decode(new(any)) != io.EOF || value.RunName != runName {
		return invalid
	}
	dispatch, exists := value.On["workflow_dispatch"]
	if !exists {
		return invalid
	}
	for _, name := range []string{"version", "expected_commit", "request_id"} {
		input, exists := dispatch.Inputs[name]
		if !exists || input.Type != "string" || input.Required != (name == "version") {
			return invalid
		}
	}
	for _, name := range []string{"prepare", "tests", "binaries", "image", "publish", "stable"} {
		if _, exists := value.Jobs[name]; !exists {
			return invalid
		}
	}
	prepare := value.Jobs["prepare"]
	if prepare.Needs.Kind != 0 || len(prepare.Steps) == 0 {
		return invalid
	}
	guard := prepare.Steps[0]
	if guard.Name != "Verify requested commit" || guard.Uses != "" || guard.If != "" || guard.ContinueOnError.Kind != 0 ||
		len(guard.Env) != 1 || guard.Env["EXPECTED_COMMIT"] != "${{ inputs.expected_commit }}" ||
		strings.Join(strings.Fields(guard.Run), " ") != strings.Join(strings.Fields(commitGuard), " ") {
		return invalid
	}
	dependencies := make(map[string]uint8, len(value.Jobs))
	for name, job := range value.Jobs {
		// A job-level condition or ignored error could bypass the prepare failure.
		if job.If != "" || job.ContinueOnError.Kind != 0 {
			return invalid
		}
		if name != "prepare" && !dependsOnPrepare(name, value.Jobs, dependencies) {
			return invalid
		}
		for _, step := range job.Steps {
			if strings.HasPrefix(step.Uses, "actions/checkout@") && step.With["ref"] != "${{ github.sha }}" {
				return invalid
			}
		}
	}
	return nil
}

func dependsOnPrepare(name string, jobs map[string]job, states map[string]uint8) bool {
	if name == "prepare" {
		return true
	}
	if states[name] != 0 {
		return states[name] == 2
	}
	value, exists := jobs[name]
	if !exists {
		return false
	}
	states[name] = 1
	var needs []string
	switch value.Needs.Kind {
	case yaml.ScalarNode:
		needs = []string{value.Needs.Value}
	case yaml.SequenceNode:
		if value.Needs.Decode(&needs) != nil {
			return false
		}
	default:
		return false
	}
	if len(needs) == 0 {
		return false
	}
	for _, dependency := range needs {
		if !dependsOnPrepare(dependency, jobs, states) {
			return false
		}
	}
	states[name] = 2
	return true
}
