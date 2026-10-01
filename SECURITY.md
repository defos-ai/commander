# Security

## Reporting vulnerabilities

Report suspected vulnerabilities privately to [security@defos.ai](mailto:security@defos.ai).
Please keep vulnerability details out of public issues.

Include the affected version or commit, reproduction steps, expected and actual
behavior, and potential impact. Redact credentials and sensitive target data.

## Trust boundaries

- MCP clients can issue operation commands and read saved data. Keep the endpoint
  within a trusted environment; protect remote access at your deployment boundary.
- The filesystem bus is shared by trusted hosts. A process with write access can
  influence commands and results. Restrict its files and the database/file-storage
  credentials to the hosts that need them.
- Actions run with the agent account's privileges. Permission grants are not an
  operating-system sandbox. Use trusted actions and least-privilege hosts.
- Child programs inherit the agent's environment. Supply only credentials required
  by that agent; report missing configuration keys, never secret values.
- Messages, records, artifacts and reports may contain sensitive data. Protect
  persistence and backups; keep secrets and runtime data out of source control.
