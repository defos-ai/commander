# Working on feature guides

Inherit the [repository guidance](../../AGENTS.md). These guides explain how
operators use Commander; [README.md](README.md) is the feature index.

- Lead with the next useful action, then show a small example and the limits
  needed to operate safely. Keep each guide short and link related workflows.
- Put mission authoring and policy configuration in `missions.md`; put running,
  inspecting and settling work in `operations.md`. Questions, secrets, sessions
  and data sharing have their own guides. Avoid repeating their full workflows.
- Use current `policies.continuation`, `policies.completion` and
  `policies.sharing` shapes. Resource ownership belongs to sessions. Mission edits affect future operations only.
- Keep component setup, flags and command references in component READMEs.
  Link implementation contracts, provider interfaces and checks in the relevant
  AGENTS files instead of copying them into feature guides.
- Preserve important boundaries: completion does not prove every task succeeded;
  a timeout does not prove work stopped; question fallback does not approve
  operator-controlled completion; secrets stay out of chat and question answers.
  Data sharing is live and best effort, without replay after receiver restart.
- Retain the existing session and sharing diagrams. Add diagrams only when they
  make a workflow clearer; do not use a diagram instead of actionable steps.
- When moving content, update incoming links and shorten the original to its
  remaining purpose. `../abstractions.md` is the glossary, not a second tutorial.

For documentation-only changes, check local links/anchors, JSON examples,
Markdown fences and README/AGENTS pairing. Application tests are unnecessary.
