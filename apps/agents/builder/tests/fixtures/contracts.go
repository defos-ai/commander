package fixtures

import (
	"encoding/json"

	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
)

const BuildSecret = "managed-codex-token-not-for-artifacts"

const BuildInstructions = "Create an agent that counts words. $(never-run-this)\nPreserve \"quoted\" instructions."

const BuildAgentType = "word-count-agent"

const BuildBinary = "commander-word-count-agent"

func BuildRequestedActions() []actionmodel.Definition {
	return []actionmodel.Definition{{
		Name: "count-words", Description: "Return the word count of supplied text",
		RequiredSecrets: []string{"SERVICE_TOKEN", "SERVICE_ACCOUNT"},
		InputSchema:     json.RawMessage(`{"type":"object","$defs":{"text":{"type":"string","minLength":1}},"properties":{"text":{"$ref":"#/$defs/text"},"options":{"type":"object","properties":{"language":{"type":"string","default":"en"}},"additionalProperties":false}},"required":["text"],"additionalProperties":false,"x-contract":{"sample":9007199254740993,"type":"business-label"}}`),
		OutputSchema:    json.RawMessage(`{"type":"object","properties":{"count":{"type":"integer","minimum":0}},"required":["count"],"additionalProperties":false}`),
	}, {
		Name: "describe-text", Description: "Describe supplied text",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"description":{"type":"string"}},"required":["description"]}`),
	}}
}

const ReleaseWorkflow = `name: Make release
run-name: Release agent ${{ inputs.version }} ${{ inputs.request_id }}
on:
  workflow_dispatch:
    inputs:
      version: {type: string, required: true}
      expected_commit: {type: string, required: false}
      request_id: {type: string, required: false}
jobs:
  prepare:
    runs-on: ubuntu-latest
    steps:
      - name: Verify requested commit
        env:
          EXPECTED_COMMIT: ${{ inputs.expected_commit }}
        run: |
          if [ -n "$EXPECTED_COMMIT" ]; then
            test "$GITHUB_SHA" = "$EXPECTED_COMMIT"
          fi
      - uses: actions/checkout@v7
        with:
          ref: ${{ github.sha }}
  tests:
    needs: prepare
    uses: ./.github/workflows/test.yml
  binaries:
    needs: [prepare, tests]
    uses: ./.github/workflows/build-binaries.yml
  image:
    needs: [tests, binaries]
    uses: ./.github/workflows/push.yml
  publish:
    needs: [prepare, binaries, image]
    runs-on: ubuntu-latest
    steps:
      - run: echo publish fixture
  stable:
    needs: [prepare, image, publish]
    uses: ./.github/workflows/push-stable.yml
`
