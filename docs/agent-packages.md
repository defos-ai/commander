# Agent packages and bootstrap

This contract defines agent packaging, native spawning and planned deployment
integrations. The `commander-example-agent` template supports preassigned agent
IDs and generates release packages in this format. The [spawner module](../apps/agents/spawner/README.md)
resolves public packages, verifies native downloads and runs a regular agent
host with `spawn-agent` and repeatable `--spawn-agents`. Docker spawning and
integration into Playground/AIO/Compose are planned next.

An agent repository publishes runnable packages; the spawner starts a selected
package using the deployment's connection settings. Builder produces source and
artifacts, and the repository's release workflow publishes packages in this format.

## Release manifest

Attach `agent.yaml` to each agent's GitHub release alongside its native binaries.
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
| `version` | Required exact release tag, matching the requested version. |
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

## Resolving a package

A reference has the form `github.com/<owner>/<repository>@<version>`. Start with
explicit `vMAJOR.MINOR.PATCH` releases; moving aliases such as `stable` and
`latest` are outside this initial contract.

For `github.com/defos-ai/commander-example-agent@v0.1.0`:

1. Fetch `/defos-ai/commander-example-agent/releases/download/v0.1.0/agent.yaml`
   from `https://github.com` and validate its version and fields.
2. In native mode, select the current OS/architecture, download the named asset
   from that release and verify its checksum before making it executable.
3. In Docker mode, ask the configured Docker Engine to pull and start the image
   by digest on the deployment's network.

Verify downloaded bytes before using or caching them; an incomplete download
must not become an executable. Checksums pin the downloaded content to the
manifest, not the trustworthiness of its author. Package sources must be trusted
by the operator. Fetching metadata or binaries must not forward deployment
credentials to the download host or its redirects.

The planned deployment integration selects the execution mode explicitly:

| Deployment | Launch behavior |
| --- | --- |
| Native `commander --all-in-one` | Child executable using the session's FS bus and artifact directories. |
| Docker AIO | Child executable inside the AIO container, using the same FS directories. |
| Compose spawner | Sibling container on Commander’s Docker network, using RabbitMQ and S3. |

Docker AIO continues to include its default agents' native prerequisites. A
Compose spawner needs access to the host's Docker Engine; spawned containers
receive no Docker socket or host-control privileges by default.

## Bootstrap and registration

The launcher supplies an explicit environment. It must not inherit all of its
own credentials into the new agent or accept deployment settings from a manifest.

| Setting | Purpose |
| --- | --- |
| `BUS_URL` | Shared absolute FS path or RabbitMQ URL. |
| `STORAGE_URL` | Shared absolute FS path or S3 artifact store URL. |
| S3 settings | The deployment's applicable `AWS_ENDPOINT_URL_S3`, `AWS_REGION`, `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN` and `S3_USE_PATH_STYLE`. |
| `COMMANDER_AGENT_ID_SUFFIX` | Exactly eight lowercase hex characters from the agent ID assigned and saved by the spawner before launch. |

Keep ordinary process settings such as executable search paths explicit too.
Spawned workload agents connect to the bus and artifact storage; this bootstrap
supplies no controller-database connection or API/MCP login. The current native
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

The native spawner matches the assigned ID and expected type to the child's
registration announcement before returning the agent ID and process reference.
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
Planned integration passes this list from Compose and native/AIO Commander to
their spawner. Defaults will use pinned packages. Startup and the controller-dispatched
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
Automatic restart recovery is not implemented.

Reconcile uncertain launches before trying again. Retain enough ownership
information to identify the exact instance; a PID or agent type alone is not
proof. Report uncertainty when identity cannot be established. Bound download,
pull and registration waits; task cancellation must not cause a blind second launch.

The spawn action finishes after bootstrap and leaves the agent running. Native
and AIO sessions own child shutdown and must await it before deleting shared FS
state. Docker containers have an operator-configured lifetime and survive a
spawner restart. They are not Compose services: deployment teardown must remove
owned containers before removing their network or data. Continuous monitoring,
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
4. Apply the same packaging to the separate web/browser repositories. Publish
   verified packages before switching Commander startup or removing bundled code.

The template remains the reference for manually developed and builder-generated
agents. Its demo Compose remains useful for development; the spawner consumes
the release manifest rather than executing repository deployment YAML.
