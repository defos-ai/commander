# Commander with agent frameworks

Use Commander as an enterprise control plane above agents built with existing
frameworks. Keep their models, tools and internal workflows, and coordinate them
through shared missions, operator decisions, execution records and artifacts.

## Connect an existing agent

1. Implement a [Commander agent wrapper](../apps/agents/README.md#add-an-action-to-an-existing-agent)
   that exposes useful capabilities as actions with JSON input and output schemas.
   The wrapper invokes your existing agent through its process or service API.
2. Map progress, questions and results to Commander's logs, execution questions
   and artifacts. Supply managed credentials only to the runtime that needs them.
3. Register the wrapper with the controller. Mission rules and model or operator
   decisions coordinate its actions alongside other agents.

The existing framework retains its internal workflow state and recovery behavior.
The wrapper owns that integration, including runtime session identifiers and how
saved execution checkpoints relate to external work.

## Framework examples

These are integration patterns to implement through the wrapper:

| Framework | How Commander can coordinate it |
| --- | --- |
| [LangChain](https://docs.langchain.com/oss/python/langchain/overview) | Expose an existing agent's model and tool loop as a mission action. |
| [LangGraph](https://docs.langchain.com/oss/python/langgraph/overview) | Run a graph workflow as an action while retaining its internal state in LangGraph. |
| [CrewAI](https://docs.crewai.com/en/introduction) | Delegate an action to a Crew or Flow and return its findings to the operation. |
| [Microsoft Agent Framework](https://learn.microsoft.com/en-us/agent-framework/overview/) | Connect an agent or workflow through a wrapper around its host application. |
| [Google ADK](https://adk.dev/get-started/about/) | Expose an ADK agent's capabilities alongside other registered Commander agents. |
| [OpenAI Agents SDK](https://developers.openai.com/api/docs/guides/agents/sdk) | Wrap an SDK agent run and report its progress, results and artifacts. |

An existing agent host can also connect as an [MCP client](../apps/adapters/README.md#quick-start)
and make mission decisions while Commander dispatches work to registered agents.
This supports continuously running decision makers beyond Playground.
