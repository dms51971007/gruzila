# gruzilla-backend

Backend facade for Gruzilla UI.

It accepts frontend `POST /api/v1/*` requests and executes `gruzilla-cli` commands.
The backend does not call executor APIs directly.

## Run

From `C:\projects\load\gruzilla`:

```powershell
go run ./gruzilla-backend
```

Default listen address: `:8080`.

Run with explicit config path:

```powershell
go run ./gruzilla-backend --config ".\config-backend.yml"
```

Default config file: `config-backend.yml` in current working directory.

## Environment

- `GRUZILLA_BACKEND_ADDR` (default `:8080`)
- `GRUZILLA_BACKEND_CONFIG` (default `config-backend.yml`)
- `GRUZILLA_CLI_COMMAND` (default `go`)
- `GRUZILLA_CLI_ARGS` (default `run ./cmd/gruzilla-cli`)
- `GRUZILLA_CLI_WORKDIR` (default `.`)
- `GRUZILLA_CLI_TIMEOUT_SECONDS` (default `30`)
- `GRUZILLA_DEFAULT_EXECUTOR_URL` (default `http://localhost:8081`)

Environment variables override values loaded from `config-backend.yml`.

## Request tracing

- Reads incoming header `X-Request-Id`.
- If missing, generates UUID-like id.
- Passes value to CLI via `--request-id`.
- Returns `X-Request-Id` in response headers.
