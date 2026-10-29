# Commander MCP adapter

Connect AI clients to Commander missions through the Model Context Protocol.
Submit operation commands to the controller and inspect saved progress, results
and artifacts.

Follow the [MCP setup and workflow guide](https://github.com/defos-ai/commander/blob/main/apps/adapters/README.md#quick-start)
for authentication, client configuration and tool usage.

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
