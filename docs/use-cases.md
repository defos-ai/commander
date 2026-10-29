# Commander use cases

Commander (`defos-ai/commander`) gives teams a control plane for coordinating AI
agents across engineering, security, business systems and research. A mission
defines objectives, execution rules and completion authority; agents connect
existing tools and systems, while saved results and artifacts support review.

Use these examples to design missions with suitable agents and integrations.
Install or implement the required actions for your systems before running them.

## 11 practical use cases

| Area | Use case | Example mission |
| --- | --- | --- |
| Cybersecurity | Autonomous security operations | Investigate a suspicious IP across SIEM, EDR, cloud logs and threat intelligence. |
| Red teaming | Authorized security testing | Discover assets, identify exposures, validate vulnerabilities within scope and generate a report. |
| Compliance | Continuous compliance monitoring | Map controls to NIST, PCI DSS or DORA, collect evidence and prepare audit reports. |
| Software development | Multi-agent development | Analyze requirements, generate code, run tests, review security and prepare a pull request. |
| DevOps / SRE | Incident response automation | Investigate service failures, correlate logs and metrics, suggest fixes and execute approved remediation. |
| Sales | Prospect research and outreach | Identify target companies, enrich leads, research decision-makers and prepare personalized outreach. |
| Market intelligence | Competitive intelligence | Monitor competitors, analyze product changes, compare positioning and produce reports. |
| Data engineering | Distributed data processing | Retrieve data from multiple sources, validate quality, transform records and publish results. |
| Customer support | Complex ticket resolution | Investigate an issue across CRM, documentation, billing and infrastructure systems. |
| Industrial / OT | Infrastructure monitoring | Correlate operational anomalies, security events and maintenance records across sites. |
| Universities / R&D centers | Long-running research programs | Coordinate literature reviews, collect and analyze data, track experiments, and update findings and reports over weeks or months. |

## Build a mission

1. [Build or install agents](../apps/agents/README.md) with actions for the systems
   involved, and configure their access.
2. [Define a mission](abstractions.md#create-a-mission) with objectives and required
   actions. Choose [rules](abstractions.md#mission-rules) for follow-up work and
   model review, and include human review where the process needs approval.
3. Start an operation through Playground, MCP or the API. Use
   [Admin](../apps/admin/ui/README.md) to inspect its tasks, execution logs, results
   and artifacts.
