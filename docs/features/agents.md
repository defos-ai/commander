# Agents

Open **Admin → Agents**, or use MCP's `list_agents` and `describe_agent`, to
inspect each instance's ID, type, reported version, advertised hostname, current
work and action readiness. **Unknown** means the agent did not report a package
version; Docker tags and dependency versions do not fill it in.

## Choose a spawner

Read the spawners' `server.hostname` values with `list_agents`. Start an
`install-agent` operation, then call MCP `assign_task` with the selected host:

```json
{
  "operation_id": "<install-agent-operation-id>",
  "action_name": "spawn-agent",
  "selector": {"hostname": "k8s-spawner"},
  "input": {"package": "github.com/defos-ai/commander-example-agent@stable"}
}
```

Replace the example hostname with the spawner's advertised value. Keep the filter
beside action input; the controller chooses the matching executor. See
[executor filters](operations.md#choose-an-executor) for matching and follow-ups.

To inspect on that host first, assign `inspect-agent` with the same selector.
Resolve its continuation with `actions: []`, then assign `spawn-agent` with the
selector again; different-action continuations do not inherit it.

Hostname identifies the agent's own host or container/Pod namespace. Selecting a
Kubernetes spawner chooses its runtime; Kubernetes still chooses the child's node.
Set `SERVER_HOSTNAME` on a spawner when it needs a stable advertised name. This
override is not forwarded to children, which report their own hostname. Use each
launch result's `agent_id` for later stop requests.

## Stop an instance or group

In Admin, open an agent and choose **Stop agent**, or choose **Stop agents** in
the list and enter exact ID, type, version or hostname filters. Review the
criteria and confirm the request. List search does not select which agents stop.

With MCP, call `stop_agent`, for example:

```json
{"selector":{"type":"browser-agent","version":"v1.2.3","hostname":"browser-worker"}}
```

The HTTP API accepts the same body at `POST /api/v1/agents/stop`. Supply at least
one filter; all supplied filters must match. Use `{"selector":{"id":"browser-agent-0123abcd"}}`
for one instance. A version-only selector can match several agent types.

The controller captures matching instances once. Later instances are unaffected.
Here, `stable` and `latest` are literal reported version values; they do not
resolve GitHub releases as installation references do.

- **Stop requested** (`stop_requested`): new work is blocked while accepted work
  drains. The agent may still be running.
- **Stopped** (`stopped`): shutdown was confirmed. A stale heartbeat, timeout or
  missing retained record does not confirm shutdown.

Review every returned agent and error; a group request can partially succeed.
Use Admin's **Read saved state**, MCP `describe_agent`, or `GET /api/v1/agents/{id}`
to inspect progress. After an uncertain response, read saved state before any
new request. Do not retry automatically. Current inventory cannot reconstruct
the original selection if its response was lost.

## Replace an agent

1. Choose a published release and inspect its package and dependencies using
   [the installation workflow](../agent-packages.md#sources-and-stable-references).
2. Stop the old instance or group and wait for confirmed `stopped` status.
3. Start a new `install-agent` operation with an exact package reference, such as
   `github.com/defos-ai/commander-browser-agent@v1.2.4`, using a version the
   publisher has released. This creates a new instance with a fresh ID.
4. Check the new ID, reported version and action readiness before assigning work.
   Resolve missing dependencies or [secrets](secrets.md) as needed.

Commander does not upgrade an existing instance in place. For agents deployed
directly as services, update their binary or image and restart settings through
your deployment tooling, using a fresh ID for each new process.

Stopping does not remove containers or Pods, change restart policies, or move
agent-owned [sessions](sessions.md) to a replacement. A host supervisor may start
another process. Keep execution records and working data while children exist;
follow [runtime shutdown guidance](../../apps/agents/spawner/README.md#stop-or-replace-agents)
and [scoped teardown](../deployment.md#stopping-and-resetting-agents) when removing resources.
