# Agent packages and bootstrap

This contract defines agent packaging, native/Docker spawning and deployment
integrations. The `commander-example-agent` template supports preassigned agent
IDs and generates release packages in this format. The [spawner module](../apps/agents/spawner/README.md)
resolves public packages and runs a regular agent host with `spawn-agent` and
repeatable `--spawn-agents`. Compose starts the spawner in Docker mode by default. Playground and Docker AIO
embed the spawner in native mode.

An agent repository publishes runnable packages; the spawner starts a selected
package using the deployment's connection settings. Builder produces source and
artifacts, and the repository's release workflow publishes packages in this format.

## Release manifest

Attach `agent.yaml` to each agent's GitHub release alongside any native binaries.
Generate the published manifest after building the binaries and pushing the
Docker image. This example uses illustrative checksums and an image digest;
release workflows must substitute the actual values.

```yaml
schema_version: 1
type: example-agent
version: v0.1.0
native:
  linux/amd64:
    asset: commander-example-agent_linux_amd64
    sha256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
  darwin/arm64:
    asset: commander-example-agent_darwin_arm64
    sha256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
  windows/amd64:
    asset: commander-example-agent_windows_amd64.exe
    sha256: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
docker:
  image: ghcr.io/defos-ai/commander-example-agent@sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd
```

| Field | Contract |
| --- | --- |
| `schema_version` | Required integer `1`; reject unsupported versions. |
| `type` | Required agent type, matching the type registered by the executable. |
| `version` | Required version such as `v0.2.0`, matching the requested version without any module-path prefix. |
| `native` | Optional map of `<GOOS>/<GOARCH>` to one executable asset and its SHA-256 checksum. |
| `docker.image` | Optional fully qualified registry image reference pinned by `@sha256:<digest>`. |

Require at least one nonempty delivery mode. Reject unknown fields, duplicate
keys, multiple YAML documents and incomplete entries. Native checksums and image
digests contain exactly 64 lowercase hexadecimal characters. An asset is a file
name in the same GitHub release, never a URL, path or archive. Native executables
run directly; Docker images supply their own entrypoint.

The resolver accepts manifests up to 64 KiB and native downloads up to 512 MiB,
with 30-second and five-minute deadlines respectively. YAML aliases and anchors
are unsupported. Downloads stay on HTTPS GitHub release hosts and use no
deployment credentials or environment proxies.

The package advertises only platforms it actually publishes. An unavailable
mode or platform is an explicit error. The spawner does not build source or run
installation scripts. Native prerequisites such as Chromium remain in the agent's
README and discovery results; container images include their runtime dependencies.
Actions and required secret names remain in normal agent registration.

### Source template

Use [`framework/agent/manifest`](../framework/agent/manifest/manifest.go) when
preparing a release manifest. `Render` validates a `Manifest` and returns YAML
bytes from its embedded [`agent.yaml.tmpl`](../framework/agent/manifest/agent.yaml.tmpl),
with deterministic platform ordering and quoted strings. For example, after
computing `checksum` from a built binary:

```go
data, err := manifest.Render(manifest.Manifest{
    SchemaVersion: 1,
    Type:          "example-agent",
    Version:       "v0.1.0",
    Native: map[string]manifest.NativeAsset{
        "linux/amd64": {Asset: "commander-example-agent_linux_amd64", SHA256: checksum},
    },
})
```

Import `github.com/defos-ai/commander/framework/agent/manifest`. Add a
`Docker: &manifest.Docker{Image: imageDigest}` field for a fully qualified,
digest-pinned image. Leave an absent mode `nil`; explicitly empty modes are
invalid. At least one mode is required.

`Parse(data, requestedVersion)` strictly validates downloaded manifests and
checks the requested version. Rendering and parsing share the same schema, identity,
asset, checksum, image and 64 KiB size constraints. The spawner uses this parser;
release tooling computes actual checksums, obtains the pushed image digest from
the same tested revision, handles render errors, and writes/uploads `agent.yaml`.
Placeholders and moving image tags are not release metadata.

The next Commander release will publish a separate package release for each
built-in agent, using its existing module tag. Both releases contain `agent.yaml`:

| Package | Release tag for `v0.2.0` | Delivery modes |
| --- | --- | --- |
| `github.com/defos-ai/commander/apps/agents/builder@v0.2.0` | `apps/agents/builder/v0.2.0` | Docker, `linux/amd64` |
| `github.com/defos-ai/commander/apps/agents/spawner@v0.2.0` | `apps/agents/spawner/v0.2.0` | Native Linux/macOS/Windows on amd64/arm64; Docker, `linux/amd64` |

Their manifests use the actual native checksums and image digests from the same
release run. These packages become available only after that release publishes;
existing releases are unchanged. See [release publication](releases.md#agent-packages).

## Resolving a package

A reference has the form
`github.com/<owner>/<repository>[/<module-path>]@<version>`. Use explicit
`vMAJOR.MINOR.PATCH` versions; moving aliases such as `stable` and `latest` are
outside this contract. A repository package uses the root version tag; a module
package uses `<module-path>/<version>`. Every package resolves `agent.yaml` from
that tag's release in the same repository.

For `github.com/defos-ai/commander-example-agent@v0.1.0`:

1. Fetch `/defos-ai/commander-example-agent/releases/download/v0.1.0/agent.yaml`
   from `https://github.com` and validate its version and fields.
2. In native mode, select the current OS/architecture, download the named asset
   from that release and verify its checksum before making it executable.
3. In Docker mode, ask the configured Docker Engine to pull and start the image
   by digest on the deployment's network.

For `github.com/defos-ai/commander/apps/agents/spawner@v0.2.0`, fetch
`/defos-ai/commander/releases/download/apps%2Fagents%2Fspawner%2Fv0.2.0/agent.yaml`
from `https://github.com`. Its manifest version remains `v0.2.0`, and native
assets come from that same module-tag release. The module path selects the
release; it does not change the manifest filename or registered agent type.

Verify downloaded bytes before using or caching them; an incomplete download
must not become an executable. Checksums pin the downloaded content to the
manifest, not the trustworthiness of its author. Package sources must be trusted
by the operator. Fetching metadata or binaries must not forward deployment
credentials to the download host or its redirects.

Deployment selects the execution mode explicitly:

| Deployment | Launch behavior |
| --- | --- |
| Standalone spawner | Native by default; set `SPAWNER_RUNTIME=docker` and `SPAWNER_DOCKER_NETWORK=<network>` for Docker. |
| Compose spawner | Sibling container on Commander’s Docker network, using RabbitMQ and S3. Starts by default. |
| Native `commander --all-in-one` | Child executable using the session's FS bus and artifact directories. |
| Docker AIO | Child executable inside the AIO container, using the same FS directories; no Docker socket. |

Docker AIO continues to include its default agents' native prerequisites. A
Compose spawner needs access to the host's Docker Engine. Spawned containers use
the image's entrypoint, no mounts or published ports, dropped capabilities and
`no-new-privileges`. An image that declares volumes is rejected. Docker settings
come from the operator; packages cannot supply privileged deployment arguments.

## Bootstrap and registration

The launcher supplies an explicit environment. It must not inherit all of its
own credentials into the new agent or accept deployment settings from a manifest.

| Setting | Purpose |
| --- | --- |
| `BUS_URL` | Shared absolute FS path or RabbitMQ URL. |
| `STORAGE_URL` | Shared absolute FS path or S3 artifact store URL. |
| S3 settings | The deployment's applicable `AWS_ENDPOINT_URL_S3`, `AWS_REGION`, `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN` and `S3_USE_PATH_STYLE`. |
| Native browser settings | The deployment's `CHROMIUM_PATH` and `CHROMIUM_NO_SANDBOX`, when set. |
| `COMMANDER_AGENT_ID_SUFFIX` | Exactly eight lowercase hex characters from the agent ID assigned and saved by the spawner before launch. |

Keep ordinary process settings such as executable search paths explicit too.
Spawned workload agents connect to the bus and artifact storage; this bootstrap
supplies no controller-database connection or API/MCP login. The current
spawner does not distribute `SECRETS_ENCRYPTION_KEY`. Future managed-secret
bootstrap must be configured by the operator; a manifest cannot grant this
access. Secret values stay out of package files, action inputs,
results and logs. Existing encrypted secret delivery remains responsible for
action secrets after registration.

The agent host combines nonempty `COMMANDER_AGENT_ID_SUFFIX` with its fixed type
to form `AgentConfig.ID`: `0123abcd` becomes `example-agent-0123abcd`. Use
`id.AgentIDFromSuffix` to validate and construct it. Reject full IDs, prefixes,
uppercase hex and any value other than eight lowercase hex characters before
opening providers. When unset or empty, the host generates a fresh ID.
Registration and saved launch records still contain the complete ID; only the
suffix crosses the process bootstrap boundary. No additional public configuration
field is needed.

The spawner matches the assigned ID and expected type to the child's registration
announcement before returning the agent ID, runtime and PID or container ID.
This observes the shared bus, not confirmation of the controller's database write.
The controller independently persists registration and evaluates eligibility.
Another running agent of the same type cannot satisfy this check. The spawner
must prevent two processes from using the same ID; registration does not prove
exclusive process ownership. The identifier is not an authentication credential.

Registration and action readiness are separate results. Report missing
requirements, including secrets or Chromium, without removing an otherwise
successfully registered agent. A dependent mission can proceed only when its
required actions are ready.

## Startup, retries and lifetime

The spawner host's `--spawn-agents` is repeatable. Each occurrence requests one
instance, including repeated references when two instances of a type are needed.
Compose passes one browser and two web `v0.2.0` references as its spawner service's
command. Native/AIO Commander
defaults to `github.com/defos-ai/commander-web-agent@v0.2.0` and
`github.com/defos-ai/commander-browser-agent@v0.2.0`. Its repeatable `--spawn-agents`
replaces those defaults; `--spawn-agents=` skips startup packages while keeping
the embedded builder and spawner. Each temporary session needs Internet access
for its configured package downloads. See [Playground](../apps/playground/README.md#startup-agents).
Startup and the controller-dispatched
`spawn-agent` action use the same package resolution and launch implementation.

A stable launch request identifies an instance across retries; its agent ID must
be allocated and recorded before starting the process/container. Startup requests
are scoped to a deployment and configured instance; runtime requests are tied to
their saved task identity. Repeating a package reference in two startup slots
creates two identities, not a retry. A conflicting package for an existing launch
request fails explicitly instead of silently replacing the agent.

Retries reuse the saved launch record and agent ID to find the existing instance.
They must not start a second process with that ID. A new or replacement launch
gets a fresh agent ID; the ID alone does not provide idempotency.

The initial native implementation reconciles only processes it still owns in
memory. Its create-only launch intents survive every restart, including clean
shutdown; encountering an old intent reports uncertainty instead of adopting a
PID or launching a replacement. Inspect the old instance before requesting
replacements with a new state directory.
Native restart recovery is not implemented.

Docker records also hold the image digest, network and a random ownership label.
Retries inspect the recorded container and verify its launch configuration before
reusing it, including after a spawner restart. A fresh host then waits for a fresh
agent announcement. Lost Create/Start responses keep the intent for reconciliation;
they never trigger a second Create. A missing container reports uncertainty; an
exited container requires a new launch request. Only a never-started container may
start under its saved identity. An OS lock permits one active Docker spawner per
state directory, preventing concurrent Start calls from racing across hosts.

Reconcile uncertain launches before trying again. Retain enough ownership
information to identify the exact instance; a PID or agent type alone is not
proof. Report uncertainty when identity cannot be established. Bound download,
pull and registration waits; task cancellation must not cause a blind second launch.

The spawn action finishes after bootstrap and leaves the agent running. Native
and AIO sessions own child shutdown and must await it before deleting shared FS
state. Docker containers survive a spawner restart, with automatic container
restarts disabled. They are not Compose services: deployment teardown must remove
owned containers before removing their network or data. See the
[Compose teardown commands](deployment.md#stopping-and-resetting-agents). Continuous monitoring,
scaling, automatic upgrades and replacement are outside the spawner's role.

Initially, runtime spawning uses a separate provisioning operation. Current
[mission preflight](../framework/controller/runner/scheduler.go) blocks all
dispatch in a mission whose required actions are unavailable, including a task
that would spawn the missing agent.

## Adoption order

1. Update `commander-example-agent` to accept a preassigned agent ID suffix,
   keep bus/storage bootstrap settings consistent, and include a manifest template.
2. Add its binary/image release workflow. Validate and publish native assets,
   image digest and generated `agent.yaml` from the same tested revision. Keep the
   release unpublished until all package assets are available. Supply README and
   AGENTS instructions, with tests and fixtures in `tests/`.
3. Verify that an agent built from the template can be spawned natively and through
   Docker, including exact-instance registration and repeat-request behavior.
4. Keep the separate web/browser repositories on the same package contract.
   Commander startup uses their `v0.2.0` packages. Their source and action tests
   stay in those repositories; Commander tests consume pinned release images or
   Go modules for cross-host integration.

The template remains the reference for manually developed and builder-generated
agents. Its demo Compose remains useful for development; the spawner consumes
the release manifest rather than executing repository deployment YAML.
