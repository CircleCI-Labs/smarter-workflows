# Architecture

| Service | Language | What it does |
|---|---|---|
| `services/api` | Go | Serves `GET /greeting` and `GET /healthz`. |
| `services/web` | Node | Builds and renders the same greeting in the browser. |
| `services/worker` | Python | Makes greetings in bulk. |

The api and the web client follow one schema, `libs/proto/schema.json`, and
both of their tests read it.

See the [runbook](runbook.md) to run the checks, and the [index](index.md) for
the overview.
