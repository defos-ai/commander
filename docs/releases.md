# Releasing Commander

Commander uses one version for the repository and all
[workspace modules](../go.work). Internal dependencies are pinned to `v0.1.0`
for the first release. These pins do not publish tags or artifacts; publication
still requires the checks and workflow below.

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

The release-test workflow then calls the [test pipeline](../.github/workflows/test.yml)
for the same commit. All basic checks must pass before provider, FS/full-stack
API/MCP integration tests begin. Web/browser repositories validate their own
agent-specific suites. Release validation always runs the
complete pipeline using `all: true`, bypassing changed-path selection.

## Publish

In GitHub Actions, open [Make release](../.github/workflows/make-release.yml),
choose the default branch, enter the version and select **Run workflow**.
This runs the full release-test workflow first. Only after every suite succeeds,
it builds and tests the native binaries, pushes the root and all module tags
atomically, then publishes Docker images, agent package releases and the root
GitHub Release. Every artifact uses that run's exact commit SHA. Publishing is
allowed only from the default branch. If any Git tag exists, tagging fails
without overwriting tags or publishing a partial tag set.

Use the separate release-test workflow for validation without publication.

## Native binaries

[build-binaries.yml](../.github/workflows/build-binaries.yml) builds `commander`,
`commander-spawner-agent` and `commander-builder-agent` for Linux, macOS and Windows on amd64 and arm64
runners. Each runner builds and embeds the Admin frontend in Commander, checks
environment discovery, runs the all-in-one smoke tests (including Admin), and
checks all three executables' `--help` before uploading them.

After every image and both agent package releases publish,
[push-binaries.yml](../.github/workflows/push-binaries.yml)
attaches all eighteen executables, `LICENSE` and `checksums.txt` to the root-version
GitHub Release. File names are `commander_<os>_<arch>`,
`commander-spawner-agent_<os>_<arch>` and `commander-builder-agent_<os>_<arch>`
(`.exe` for Windows); OS names are `linux`,
`darwin` and `windows`. SHA-256 checksums cover every binary and the license.
Binaries are unsigned; macOS builds are not notarized.

Download the binary as `commander` (`commander.exe` on Windows). On Linux/macOS,
run `chmod +x commander`, then `./commander --all-in-one`; on Windows, run
`.\commander.exe --all-in-one`. See
[native setup](../apps/playground/README.md#native-one-executable) for model setup,
the generated API login and temporary-state behavior. All-in-one embeds builder
and spawner agents. Install additional agents through chat/MCP.
Package downloads require Internet access. Native browser actions need Chromium
or Chrome; the builder's `checkout-template` action needs Git on the host.

The standalone spawner runs with the controller's bus and artifact storage; see
its [run instructions](../apps/agents/spawner/README.md#run). Its assets are added
by the next release using this workflow; existing releases are unchanged.

Use `/releases/latest/download/<file-name>` for a
[versionless download](https://docs.github.com/en/repositories/releasing-projects-on-github/linking-to-releases#linking-to-the-latest-release).
Use `/releases/download/<version>/<file-name>` to pin a release.

Uploads stay in a draft until all assets are attached. For a failed build or
publication, rerun failed jobs in the same workflow run; partial draft uploads
can be replaced, but published release assets cannot. Tag and image publication
cannot be rolled back by a later release-upload failure. Release creation uses
the repository's `GITHUB_TOKEN` with `contents: write`.
If publication already completed, a retry succeeds only after verifying that
the published filenames and bytes exactly match this run's assets.

## Agent packages

Starting with the next release, [push-binaries.yml](../.github/workflows/push-binaries.yml)
also publishes two agent package releases in this repository, using the existing
module tags. For a `v0.2.0` release:

| Release tag | Assets |
| --- | --- |
| `apps/agents/builder/v0.2.0` | Six `commander-builder-agent_<os>_<arch>` executables, `agent.yaml`, `LICENSE`, `checksums.txt` |
| `apps/agents/spawner/v0.2.0` | Six `commander-spawner-agent_<os>_<arch>` executables, `agent.yaml`, `LICENSE`, `checksums.txt` |

Both manifests provide all six native platforms and Docker.
Both images are `linux/amd64`, using the existing
`ghcr.io/defos-ai/commander-builder-agent` and
`ghcr.io/defos-ai/commander-spawner-agent` image names.
The [manifest generator](../.github/scripts/agent-manifest.go) calls the shared
`framework/agent/manifest.Render` with the native binary checksums and pushed
image digests from that same tested release run. Each manifest's `version` is
`v0.2.0`, without the module path. Each package's `checksums.txt` covers its
manifest, license and any native binaries.

Package references include the module path:
`github.com/defos-ai/commander/apps/agents/builder@v0.2.0` and
`github.com/defos-ai/commander/apps/agents/spawner@v0.2.0`.
See [package resolution](agent-packages.md#resolving-a-package). These packages
are available only after the next release publishes; existing releases are
unchanged. Standalone agent repositories continue to use their root version
tags and `agent.yaml`.

Both package releases remain drafts until their complete assets are attached and
are published with `Latest` disabled. Retry failures in the same workflow run;
never overwrite published assets. The root release contains all eighteen
native binaries and publishes only after both package releases succeed. Docker
`stable` and the root release's `Latest` promotion wait for all three releases.

## Docker images

Commander publishes the following images with a version tag, such as `v0.1.0`,
and a moving `stable` tag:

| Image | Workflow |
| --- | --- |
| `ghcr.io/defos-ai/commander-aio` | [push-aio.yml](../.github/workflows/push-aio.yml) |
| `ghcr.io/defos-ai/commander-playground` | [push-playground.yml](../.github/workflows/push-playground.yml) |
| `ghcr.io/defos-ai/commander-api` | [push-api.yml](../.github/workflows/push-api.yml) |
| `ghcr.io/defos-ai/commander-admin` | [push-admin.yml](../.github/workflows/push-admin.yml) |
| `ghcr.io/defos-ai/commander-mcp` | [push-mcp.yml](../.github/workflows/push-mcp.yml) |
| `ghcr.io/defos-ai/commander-controller` | [push-controllers.yml](../.github/workflows/push-controllers.yml) |
| `ghcr.io/defos-ai/commander-builder-agent` | [push-agents.yml](../.github/workflows/push-agents.yml) |
| `ghcr.io/defos-ai/commander-spawner-agent` | [push-agents.yml](../.github/workflows/push-agents.yml) |

These reusable workflows run only through **Make release**, after its tests and
tagging succeed. They publish `linux/amd64` images; Admin includes the built UI.
The all-in-one image starts Playground with its local hosts, embeds Admin in the
Commander binary and includes Chromium and Git. Builder and spawner start
by default; the spawner downloads additional requested packages inside the
container, without a Docker socket.
Publish `127.0.0.1:4275:4275` to use Admin;
its API connection stays inside the container.
The lightweight Playground image connects to an existing MCP service.
The standalone spawner image defaults to native mode; Docker mode needs the
deployment's Engine access, network and shared providers. See its
[Docker setup](../apps/agents/spawner/README.md#docker). This image will first be
published by the next release using this workflow.
There are no automatic pushes from pull requests or ordinary branch updates.
If an image fails to publish, rerun failed jobs in the same release run.
Git tags and images are separate publication steps; image failures do not undo tags.

After all images, agent packages and root release assets publish,
[push-stable.yml](../.github/workflows/push-stable.yml) points each `stable` tag
to that run's image digest without rebuilding, then marks the GitHub Release as
`Latest`. Only the highest published stable release advances these pointers;
publishing an older maintenance version leaves them unchanged.
The all-in-one image remains `ghcr.io/defos-ai/commander-aio:stable`.

The [web agent](https://github.com/defos-ai/commander-web-agent) and
[browser agent](https://github.com/defos-ai/commander-browser-agent) publish and
promote their images from their own repositories. Their image names remain
`ghcr.io/defos-ai/commander-web-agent` and
`ghcr.io/defos-ai/commander-browser-agent`; their versions are independent of
Commander releases. **Make release** must not publish or promote these images.

Tags update individually. If promotion fails partway through, rerun failed jobs
in the same release run. Pin version tags or digests when deploying several
components together.

Authentication uses the repository's `GITHUB_TOKEN` with `packages: write`.
The organization must allow package creation; existing packages must grant this
repository Actions access. New GHCR packages default to private; make them public
in package settings when opening the project. See [GitHub's container registry guide](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry).
