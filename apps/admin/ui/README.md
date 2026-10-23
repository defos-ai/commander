# Commander Admin UI

Run missions, answer questions and inspect operation results, screenshots and
files in a browser.

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

Retrieve it again after an API restart, or set `API_PASSWORD` before launch to
keep it stable. Use **Add connection** for another API. Change the published ports
with `COMMANDER_UI_PORT` and `COMMANDER_API_PORT`.

### All-in-one

The [all-in-one image](../../playground/README.md#docker-recommended) starts Admin
automatically. With a native binary, type `/admin serve` or launch with
`--all-in-one --serve-admin-ui`.

Type `/admin` for its link and `/credentials` for the all-in-one API login.
Completed operation rows link directly to their Admin details.

Docker must publish `127.0.0.1:4275:4275`. For another port or host, pass
`--admin-url http://YOUR_HOST:PORT` after the image name.

## Review operations

Choose a mission and enter its JSON input to start an operation. Open the
execution graph to inspect tasks, results and artifacts; download the final
report as JSON. Use **Manage secrets** when an agent needs a value.

Answer questions with an option or your own text. Cancelling stops queued work;
dispatched tasks may finish. After a command timeout, refresh the operation
before trying again.

## Create missions

1. Open **Missions → Create mission** and enter a name and objectives.
2. Search actions and mark them **Required** or **Optional**. Choose starting
   actions to guide the first assignments.
3. Choose **Define action** for a capability to build later. Add its input/output
   object schemas and required secret names.
4. Use **Advanced settings** for rules, planning steps and schemas, or **JSON** for
   the full definition. Save to open the new mission.

Actions can be installed later; saving does not start work. Keep secret values
out of definitions. Names are unique; use **Check saved mission** after an
uncertain save. Unsaved drafts disappear when you leave or reload the page.

Open **JSON** on a saved mission to view or copy its definition. See
[mission contracts](../../../docs/abstractions.md#mission-operation-and-task).

## Secrets

Open **Agents → Manage secrets** to provide, replace or delete an agent type's
secrets. You can also set an expiration. Values are write-only.

Compose and all-in-one configure encryption automatically. Standalone hosts need
an [encryption key](../../adapters/api/README.md#secrets). After saving, wait for
the agent's missing requirement to clear; after an uncertain write, refresh
metadata before retrying. Running tasks keep their existing deadline while waiting.

### Connections and sessions

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

### Native server

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
