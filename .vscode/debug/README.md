# Debug Compose services

Open the repository root in VS Code, install the **Go** extension (`golang.go`),
then start from the repository root:

```sh
docker compose -f compose.yaml -f .vscode/debug/compose.yaml up --build --wait
```

This replaces the controller, API and MCP in your existing development stack and
keeps its saved volumes. Select **Commander Docker: All Services** in **Run and
Debug**, or select one host, set breakpoints and press **F5**.

| Host | Attach configuration | Local port |
| --- | --- | --- |
| Controller | Commander Docker: Controller | 2345 |
| API | Commander Docker: API | 2346 |
| MCP | Commander Docker: MCP | 2347 |

Debugger ports bind to localhost. These containers enable ptrace and disable
seccomp for Delve; use normal images for regular deployment. Build for your host's
native architecture. Pausing can cause request timeouts and expired agent leases.

Run Playground against the debug stack:

```sh
docker compose -f compose.yaml -f .vscode/debug/compose.yaml run --build --rm --no-deps playground
```

Delve occupies PID 1, so retrieve generated credentials with these explicit commands:

```sh
docker compose exec api bash -ec 'while IFS= read -r -d "" entry; do export "$entry"; done < /proc/1/environ; exec commander-api credentials'
docker compose exec mcp bash -ec 'while IFS= read -r -d "" entry; do export "$entry"; done < /proc/1/environ; exec commander-mcp credentials'
```

If a breakpoint stays hollow, check the selected host and rebuild after edits.
Disconnect its debugger, rebuild, then attach again:

```sh
docker compose -f compose.yaml -f .vscode/debug/compose.yaml up -d --build --no-deps controller
```

**Disconnect** leaves the target running; continue a paused target first.
**Terminate** stops it; restart with the command above. For startup debugging,
remove that service's `--continue` in [compose.yaml](compose.yaml), start its
dependencies, then start the service with `up -d --no-deps SERVICE` and attach.

Return to normal images while retaining data:

```sh
docker compose -f compose.yaml up --build --wait
```
