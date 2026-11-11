# Commander Admin UI

Oversee distributed agent work in a browser: review execution graphs and live
logs, answer questions, approve completion and inspect reports, screenshots and
files. Connect to separate Commander APIs to manage complex operational missions
from one interface.

## Quick start

From the repository root:

```sh
docker compose up --build --wait
```

Open [localhost:4275](http://localhost:4275) and choose **Connect** under
**Local Commander detected**. Get the API password in another terminal:

```sh
docker compose exec api commander-api credentials
```

Compose retains the password across restarts. Use **Add connection** for another
API. Change the published ports with `COMMANDER_UI_PORT` and `COMMANDER_API_PORT`;
see [deployment](../../../docs/deployment.md#local-compose) to rotate the password.

### All-in-one

The [all-in-one image](../../playground/README.md#docker-recommended), native
`--all-in-one` and `commander start` serve Admin automatically. If Admin cannot
start, the other hosts keep running; `/admin serve` retries in Playground after
you fix the issue.

Type `/admin` for its link and `/credentials` for the all-in-one API login.
Completed operation rows link directly to their Admin details.

Docker must publish `127.0.0.1:4275:4275`. For another port or host, pass
`--admin-url http://YOUR_HOST:PORT` after the image name.

## Review operations

Opening a connection takes you to **Operations**. Click **Current connection**
for **Status** and its health checks. The dot is green when all checks pass, red
when any fails, and neutral before a result is available.

Choose a mission and enter its JSON input to start an operation. Open the
execution graph to inspect tasks, results and artifacts; download the final
report as JSON. Use **Manage secrets** when an agent needs a value. See the
[operation workflow](../../../docs/features/operations.md) for readiness and recovery.

Open **Logs** for recent execution progress, or use a task's **View logs** button.
Filter by task, agent or action. **Load older** reads history; **Pause following**
freezes updates and scrolling while work continues. Scrolling up also pauses.
Use **Follow live** to catch up or **Jump to latest** to return to recent entries.
The view keeps up to 1,000 logs at a time; older entries remain available through
**Load older**.

The **Questions** section shows the model waiting countdown and saved answer
origin. Choose an option or enter text; inspect a competing saved answer before
continuing. See [Questions](../../../docs/features/questions.md) for fallback behavior.

Choose **Complete operation** to review objectives and task outcomes, enter the
final output as JSON, and confirm completion. The dialog shows the mission's
output schema and any completion draft awaiting operator approval. After an
uncertain submission, choose **View operation** before submitting again. If
the completion draft changes, choose **Load latest output** to replace your draft
explicitly, then review and approve it.

## Create and edit missions

Open **Missions → Create mission** and use Form or JSON mode. **Define action**
adds a desired capability; **Advanced settings** edits policies and schemas.
Follow [Missions](../../../docs/features/missions.md) for definitions and policy choices.

Use **Check saved mission** after an uncertain save. Unsaved drafts disappear
when you leave or reload the page. Open **JSON** on a saved mission to view or copy it.

To change a saved mission, open it and choose **Edit mission**. JSON fields are
formatted for reading while preserving numeric precision and string escapes.
Update objectives, actions, completion decision or advanced settings in Form or
JSON, then save. After an uncertain update, inspect the saved mission before trying again.

## Secrets

Open **Secrets** in the sidebar to provide, replace or delete shared secrets.
You can also set an expiration. Values are write-only; follow the
[secret workflow](../../../docs/features/secrets.md) for sharing and delivery limits.

Use **One-time requests → Provide value** for verification codes and other
temporary values. Check the agent, operation and expiry before sending. Wait for
**Received by agent**, then inspect the operation for the outcome. After an
uncertain submission, use **View status**; closing the form does not resend it.

Compose and all-in-one configure encryption automatically. Standalone hosts need
an [encryption key](../../adapters/api/README.md#secrets). After saving, wait for
the agent's missing requirement to clear; after an uncertain write, refresh
metadata before retrying. Running tasks keep their existing deadline while waiting.

## Sessions

Open **Sessions** to find agent-owned resources, filter by scope or owner, and
inspect connection details, activity and retained history. An agent's **Sessions**
button filters the list to that exact owner; task details link to their saved
session or scope.

Use **Verify**, **Close session** or **Abandon session** according to the
[session lifecycle](../../../docs/features/sessions.md). Released history is read-only.
After an uncertain control, use **Inspect saved state** before making another
request.

## Connections

Saved connection names and URLs stay in your browser. Passwords stay in the UI
server's memory; restarting it requires signing in again. Use one UI backend replica.

Compose configures internal API routing automatically. For another private API,
set `UI_API_UPSTREAMS` to map the address you enter to the address the UI server
can reach:

```sh
UI_API_UPSTREAMS=commander.internal:4274=commander.internal:4274
```

The API must allow that hostname in `API_ALLOWED_HOSTS`. Hosted Admin cannot reach
an API on your laptop through `localhost`; use a server-reachable address.

For hosting, use HTTPS and set `UI_PUBLIC_ORIGIN=https://admin.example.com`,
including behind a reverse proxy.

When the platform supplies `PORT`, Admin listens on `:$PORT` instead of `:4275`.
`UI_LISTEN_ADDRESS` overrides it; `--listen-address` takes highest priority.
The Docker image uses the same settings.

## Native server

With Node 24+ and Go 1.26.7+, run from the repository root:

```sh
npm --prefix apps/admin/ui/frontend ci
npm --prefix apps/admin/ui/frontend run build
go run ./apps/admin/cmd/commander-admin serve --static-dir apps/admin/ui/frontend/dist
```

Start the [API](../../adapters/api/README.md) separately. Set `UI_LISTEN_ADDRESS`
to change the listener; `UI_STATIC_DIR` selects another frontend build. Connect
to the API in the browser; `serve` does not need CLI API credentials.

For a portable binary with the frontend embedded:

```sh
npm --prefix apps/admin/ui/frontend run build -- --outDir ../backend/static/dist --emptyOutDir
go build -tags embed_ui -o bin/commander-admin ./apps/admin/cmd/commander-admin
```

Run `./bin/commander-admin serve`. On Windows, build with
`-o bin/commander-admin.exe`.

### Standalone Docker image

```sh
docker run --rm -p 127.0.0.1:4275:4275 ghcr.io/defos-ai/commander-admin:stable
```

The image defaults to `serve`. It also provides the [Admin CLI](../README.md);
pass a command after the image name to replace `serve`.

## Development and testing

[Contributor guidance](AGENTS.md) ·
[Frontend scripts](frontend/package.json) ·
[Test setup](../../../framework/tests/README.md)
