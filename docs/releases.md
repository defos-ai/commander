# Releasing Commander

Commander uses one version for the repository and all
[workspace modules](../go.work). `v0.1.0` is the planned first version, not an
existing release. Current `v0.0.0` requirements are placeholders and block
release validation until maintainers update them.

## Version and tags

Choose a new `v0.X.Y` or `v1.X.Y` without prerelease or build suffixes;
`v0.0.0` is reserved. Publishing creates the root tag, such as `v0.1.0`, and
a tag for each module in the form `<module-directory>/<version>`, such as
`framework/v0.1.0` and `providers/fs/v0.1.0`, at the same validated commit.
The root tag identifies the overall release; there is still no root `go.mod`.
Nested tags follow Go's [subdirectory tag rules](https://go.dev/ref/mod#vcs-version).
Versions from v2 require module-path changes and are outside this workflow.

## Prepare and validate

Update every internal Commander requirement in each `go.mod` to the chosen
version, including indirect requirements, and update matching version-specific
local mappings in `go.work`. Review and commit these changes through the normal
development process. The workflows do not edit dependency files for you.

Run the [release tests](../.github/workflows/test-release.yml) manually with that
version, or run their Go test from the repository root:

```sh
RELEASE_VERSION=v0.1.0 go test -count=1 -failfast -timeout=10m -v ./.github/tests/release_test.go
```

The readiness check validates module paths, rejects `go.mod` replacements/exclusions,
requires known internal dependencies at the release version, checks workspace
mappings, and builds, vets and compiles tests for every workspace module.
It validates the workspace, not standalone module archives or remote availability.

The release-test workflow then runs **every test suite** for the same commit:
framework, all providers, controller, agent, Playground, API, MCP and Admin UI.
This includes both FS and full-infrastructure API/MCP tests and browser tests.
Regular development workflows keep their path filters; release checks run all suites.

## Publish

In GitHub Actions, open [Make release](../.github/workflows/make-release.yml),
choose the default branch, enter the version and select **Run workflow**.
This runs the full release-test workflow first. Only after every suite succeeds,
it pushes the root and all module tags atomically at that run's exact commit SHA,
then publishes the Docker images below from that same commit. Publishing is allowed
only from the default branch. If any Git tag exists, tagging fails without
overwriting tags or publishing a partial tag set.

Use the separate release-test workflow for validation without publication.
Native binaries are not published yet.

## Docker images

Images use the exact release version as their tag, for example `v0.1.0`:

| Image | Workflow |
| --- | --- |
| `ghcr.io/defos-ai/commander-api` | [push-api.yml](../.github/workflows/push-api.yml) |
| `ghcr.io/defos-ai/commander-admin` | [push-admin.yml](../.github/workflows/push-admin.yml) |
| `ghcr.io/defos-ai/commander-mcp` | [push-mcp.yml](../.github/workflows/push-mcp.yml) |
| `ghcr.io/defos-ai/commander-controller` | [push-controllers.yml](../.github/workflows/push-controllers.yml) |
| `ghcr.io/defos-ai/commander-web-quality-agent` | [push-agents.yml](../.github/workflows/push-agents.yml) |

These reusable workflows run only through **Make release**, after its tests and
tagging succeed. They publish `linux/amd64` images; Admin includes the built UI.
There are no automatic pushes from pull requests or ordinary branch updates.
If an image fails to publish, rerun failed jobs in the same release run.
Git tags and images are separate publication steps; image failures do not undo tags.

Authentication uses the repository's `GITHUB_TOKEN` with `packages: write`.
The organization must allow package creation; existing packages must grant this
repository Actions access. New GHCR packages default to private; make them public
in package settings when opening the project. See [GitHub's container registry guide](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry).
