# Commander MCP adapter

Connect AI clients and continuously running agent hosts to Commander's control
plane through the Model Context Protocol. Coordinate complex missions under saved
policies, hand decisions to operators when needed, and inspect progress, results and
artifacts across distributed agent work.

Follow the [MCP setup and workflow guide](https://github.com/defos-ai/commander/blob/main/apps/adapters/README.md#quick-start)
for authentication, client configuration and tool usage.

`start_operation`, `resolve_continuation`, `answer_question`, `complete_operation`,
`fail_operation` and `cancel_operation` return concise confirmations with the
operation ID, status and progress. Use `get_operation` for saved state and questions,
`get_task` for task results and artifacts, and `get_report` for final output.

Call `stop_agent` with a selector, for example
`{"selector":{"type":"browser-agent","version":"v1.2.3"}}`.
Supply at least one exact `id`, `type`, `version` or `hostname`; supplied fields intersect.
The controller captures matches once. Inspect per-agent errors and use
`describe_agent` to confirm `stopped`; `stop_requested` lets accepted work drain.
Read saved state after uncertainty before submitting again. Deployment resources
and restart policies remain host-managed.

To target a spawner or another executor, pass an optional `selector` to
`assign_task`, such as `{"hostname":"native-host"}`.
Read advertised `server.hostname` values with `list_agents`. The controller
chooses among matching ready agents that provide the action; no match leaves the
task queued.

See [Operations](../../../docs/features/operations.md) and
[Questions](../../../docs/features/questions.md) for the operator workflows.

The server listens on `127.0.0.1:4273` locally. On a platform that supplies `PORT`,
it binds to `0.0.0.0:$PORT`. Override this with `MCP_LISTEN_ADDRESS` or
`--listen-address` (highest priority). The Docker image defaults `PORT` to `4273`.
Set `MCP_ALLOWED_HOSTS` to a comma-separated list of hostnames/IPs without ports
when deploying behind a proxy, including your public and internal upstream names.
`--allowed-hosts` overrides it; the default is `localhost,127.0.0.1,::1`.
The configured list replaces those defaults; retain loopback names for local
and all-in-one clients.

To edit a mission, read `describe_mission`, then call `update_mission` with its
complete definition and unchanged name. Edit objectives, actions, policies and
schemas; existing operations keep their original mission snapshot. After an
uncertain update, read the saved definition before retrying.

To remove a reusable definition, call `delete_mission` with its `mission_name`.
Existing operations and their history remain available; new operations cannot
start from the deleted definition. Read `describe_mission` after an uncertain
deletion before retrying.

Call `complete_operation` with output matching the saved mission schema. With
omitted/model completion policy, it saves the final report and completes
immediately. With `policies.completion.decision_by: operator`, it saves `completion_draft`
and returns **awaiting operator approval** without a final report. Read the draft
with `get_operation`. Ask the operator to review or edit the output and choose
**Complete operation** in
[Admin UI](../../admin/ui/README.md#review-operations), or submit it through the
authenticated HTTP API. MCP cannot approve completion, and question fallback
does not grant approval. While awaiting review, inspect `get_operation` instead
of repeatedly submitting the same output. Read saved state after an uncertain
write before submitting again.

For an operation question, direct the operator to Admin's **Questions** section.
Use the returned `fallback_at` to schedule a fresh `get_operation`; if the
question is still pending at that time, the model may call `answer_question`.
The MCP client owns this revisit: the deadline itself emits no operation event
and does not create an answer. Inspect saved state after an uncertain write.

Explore [11 practical Commander use cases](https://github.com/defos-ai/commander/blob/main/docs/use-cases.md):
security operations, authorized security testing, compliance monitoring,
multi-agent development, incident response, prospect research and outreach,
competitive intelligence, data processing, ticket resolution, infrastructure
monitoring and long-running research for universities and R&D centers.
