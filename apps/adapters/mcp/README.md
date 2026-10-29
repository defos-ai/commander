# Commander MCP adapter

Connect AI clients and continuously running agent hosts to Commander's control
plane through the Model Context Protocol. Coordinate complex missions under saved
rules, hand decisions to operators when needed, and inspect progress, results and
artifacts across distributed agent work.

Follow the [MCP setup and workflow guide](https://github.com/defos-ai/commander/blob/main/apps/adapters/README.md#quick-start)
for authentication, client configuration and tool usage.

To edit a mission, read `describe_mission`, then call `update_mission` with its
complete definition and unchanged name. Edit objectives, actions, rules and
schemas; existing operations keep their original mission snapshot. After an
uncertain update, read the saved definition before retrying.

Call `complete_operation` with output matching the saved mission schema. With
omitted/model completion policy, it saves the final report and completes
immediately. With `completion.decision_by: operator`, it saves `completion_draft`
and returns **awaiting operator approval** without a final report. Ask the
operator to review or edit the output and choose **Complete operation** in
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
