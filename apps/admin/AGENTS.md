# Working on Admin

Admin is one Go module rooted here. Read the [root guide](../../AGENTS.md) and
the [UI guide](ui/AGENTS.md) for UI changes.

- Keep UI backend packages under `ui/backend/` and frontend sources under
  `ui/frontend/`. Do not add nested Go modules for individual interfaces.
- [docker/Dockerfile](docker/Dockerfile) builds the existing UI host with embedded
  frontend assets. The published image remains `commander-admin`.
- Playground imports the UI host from this module. Update its module requirement,
  workspace mapping and CI selection when changing Admin dependencies.
- Keep module-wide Go checks rooted at `apps/admin`; frontend commands still run
  from `ui/frontend`. Use the UI guide for packaging and integration checks.
