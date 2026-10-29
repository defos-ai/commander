# Release Commander

Use one stable `v0.X.Y` or `v1.X.Y` version for Commander and every module in
[go.work](../go.work). The prepared version is `v0.3.0`; the examples below use it.

See [release notes](release-notes.md) for Commander and standalone agent changes.

## Prepare and validate

1. Update every internal Commander requirement in each `go.mod`, including
   indirect dependencies, and the matching local mappings in `go.work`.
2. Review and commit the changes before publishing.
3. Run **Release readiness tests** in GitHub Actions with that version for validation without
   publication. For the local module readiness check, run:

```sh
RELEASE_VERSION=v0.3.0 go test -count=1 -failfast -timeout=10m -v ./.github/tests/release_test.go
```

The local check does not replace the full CI pipeline. Review the
[Release readiness tests](../.github/workflows/test-release.yml) for the required suites.

## Publish

1. Open **Actions → Make release** on the default branch.
2. Enter the prepared version and select **Run workflow**.
3. Wait for validation, native builds, tags, images and release assets to complete.
4. Confirm the root release is **Latest** and the expected images have `stable` tags.

[Make release](../.github/workflows/make-release.yml) publishes the root tag and
`<module-directory>/<version>` tags from the same tested commit. Do not create tags
manually or trigger individual publishing workflows.

### If a job fails

Rerun failed jobs **in the same workflow run**. Do not overwrite published assets
or delete and recreate tags. Images and tags may already exist even if a later job
failed. `stable` and Latest move only after the complete release publishes;
older maintenance releases do not move them backward.

## Native binaries

From v0.3.0, the root release includes `commander_<os>_<arch>` and
`commander-admin_<os>_<arch>` for Linux, macOS and Windows on amd64 and arm64.
Download standalone builder and spawner binaries from their
[agent package releases](#agent-packages). The existing v0.2.0 release also
includes copies of those agent binaries.

Windows filenames end in `.exe`; macOS uses `darwin`. `LICENSE` and
`checksums.txt` accompany the binaries. Binaries are unsigned; macOS builds are
not notarized.

Commander and Admin embed the built frontend; `commander-admin serve` starts
the standalone UI. For Commander download and run commands, use
the [quick start](../README.md#binary). Use `releases/latest/download/<filename>`
for the current release, or `releases/download/<version>/<filename>` to pin one.

## Agent packages

The publication workflow also creates separate builder and spawner package releases:

| Package reference | Release tag |
| --- | --- |
| `github.com/defos-ai/commander/apps/agents/builder@v0.3.0` | `apps/agents/builder/v0.3.0` |
| `github.com/defos-ai/commander/apps/agents/spawner@v0.3.0` | `apps/agents/spawner/v0.3.0` |

Each contains its six native binaries, `agent.yaml`, `LICENSE` and `checksums.txt`.
Both support Docker on `linux/amd64`. These references become installable after
publication completes. Verify the assets before announcing a package as
installable; existing releases are not updated.
See [packaging your own agent](agent-packages.md#release-manifest).

## Docker images

Images use `ghcr.io/defos-ai/<name>:<version>` and a moving `stable` tag:

| Image | Use |
| --- | --- |
| `commander-aio` | Playground, Admin and local hosts in one container |
| `commander-playground` | Playground client for an existing MCP server |
| `commander-admin` | Admin UI and CLI; defaults to `serve` |
| `commander-api` | HTTP API |
| `commander-mcp` | MCP server |
| `commander-controller` | Default controller |
| `commander-builder-agent` | Agent builder |
| `commander-spawner-agent` | Agent spawner |

Images are `linux/amd64`. Pin versions or digests when deploying several components
together. Web and browser agents publish from their own repositories; Commander
does not publish or promote their images.

### Registry setup

Before the first release:

- Allow Actions to write repository contents and packages.
- Grant this repository Actions access to any existing GHCR packages it publishes.
- Make packages public if anonymous users should pull them; new GHCR packages
  default to private.

For `permission_denied: write_package`, check the package's Actions access before
retrying the failed job. See [GitHub's registry guide](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry).
