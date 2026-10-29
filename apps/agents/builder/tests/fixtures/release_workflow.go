package fixtures

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
