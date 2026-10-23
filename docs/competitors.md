# Commander and other agent frameworks

Commander combines a Go agent framework with a runnable mission system:
controller dispatch, chat/MCP, Admin and saved artifacts. These recommendations
reflect that focus; each framework name links to its official documentation.

| Framework | How Commander differs | Choose Commander when… |
| --- | --- | --- |
| [LangChain](https://docs.langchain.com/oss/python/langchain/overview) | LangChain provides model/tool integrations and agent loops. Commander adds a mission runtime that dispatches schema-defined actions to independently running agents. | You want HTTP and browser workers to review a website, with findings and screenshots available together in Admin. |
| [LangGraph](https://docs.langchain.com/oss/python/langgraph/overview) | LangGraph gives you explicit control over stateful graph execution. Commander coordinates action proposals through mission rules and selects ready agent instances. | You need capability-based task routing, result aggregation and related tasks kept on the same worker. |
| [CrewAI](https://docs.crewai.com/en/introduction) | CrewAI combines role-based agent teams with event-driven Flows. Commander organizes work around executable action contracts, registered workers and saved mission runs. | You want reusable tool workers to report readiness and execute missions through chat or an API. |
| [Microsoft Agent Framework](https://learn.microsoft.com/en-us/agent-framework/overview/) | Microsoft provides agent, workflow and integration building blocks. Commander supplies a mission runtime with separate planning, controller dispatch and worker execution. | You want a supplied chat and Admin interface to coordinate scanners, browser automation and build workers. |
| [Google ADK](https://adk.dev/get-started/about/) | ADK provides agent development, workflows, sessions and deployment tooling. Commander centers execution on mission requirements, action readiness and controller-selected workers. | You want to build or install agents from GitHub packages and use their capabilities in missions. |
| [OpenAI Agents SDK](https://developers.openai.com/api/docs/guides/agents/sdk) | The SDK runs model/tool loops and handoffs inside your application. Commander separates the model planner from controller-routed worker actions and operation records. | You want a model to plan work while independently running agents execute tools and save results for review in Admin. |

Other frameworks also support persistence, human input and deployment. Choose by the runtime and workflow you want to operate.
