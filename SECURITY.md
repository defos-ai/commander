# Security

## Reporting vulnerabilities

Report suspected vulnerabilities privately to [security@defos.ai](mailto:security@defos.ai).
Please keep vulnerability details out of public issues.

In the private report, include the affected version or commit, reproduction
steps, expected and actual behavior, and potential impact. Redact credentials
and sensitive target data from logs and examples.

## Trust boundaries

- The controller and agents share a local filesystem bus and state directory.
  Restrict that directory to trusted users and processes: writers can influence
  commands, state, and results.
- Actions run in the agent process or launch host programs with the agent account's
  privileges. Permission grants and executable search paths do not provide an
  operating system sandbox. Use trusted actions and the least privileges needed.
- Child programs inherit the agent's environment, which may contain credentials.
  Supply only the credentials needed by that agent and its actions.
- Bus messages, operation records, and artifacts may contain sensitive action
  output or target data. Protect the state directory and its backups, and keep
  credentials and sensitive runtime data out of source control and public reports.
