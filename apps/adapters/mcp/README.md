# Commander MCP adapter

Connect AI clients to Commander missions through the Model Context Protocol.
Submit operation commands to the controller and inspect saved progress, results
and artifacts.

Follow the [MCP setup and workflow guide](https://github.com/defos-ai/commander/blob/main/apps/adapters/README.md#quick-start)
for authentication, client configuration and tool usage.

To edit a mission, read `describe_mission`, then call `update_mission` with its
complete definition and unchanged name. Edit objectives, actions, rules and
schemas; existing operations keep their original mission snapshot. After an
uncertain update, read the saved definition before retrying.

When the saved mission sets `completion.decision_by` to `operator`, ask the
operator to review the final output and choose **Complete operation** in
[Admin UI](../../admin/ui/README.md#review-operations), or submit it through the
authenticated HTTP API. MCP cannot approve completion; question answers and
fallback do not grant this approval.

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
