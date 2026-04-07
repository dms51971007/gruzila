# Gruzilla Project Schema

`gruzilla` is a load-testing platform with three runtime layers:

1. `gruzilla-frontend` - browser UI for operators.
2. `gruzilla-backend` - HTTP facade that validates input and delegates actions to CLI.
3. `gruzilla-executor` - process that executes scenario steps and exposes runtime API/metrics.

The backend and executor are independent processes. Backend does not call executor internals directly; it runs `gruzilla-cli`, and CLI talks to executor HTTP API.

## 1) Top-Level Workspace Map

```text
gruzilla/
|-- cmd/
|   |-- gruzilla-cli/            # CLI: run/executors/scenarios/templates commands
|   `-- gruzilla-executor/       # Executor entrypoint (loads scenario, starts HTTP API)
|-- internal/
|   |-- api/                     # Unified JSON API response helpers
|   |-- executor/                # Core runtime: run loop, step handlers, clients, metrics
|   |-- scenario/                # YAML scenario model + validation + loading
|   `-- templates/               # Template rendering/cache for step payloads
|-- gruzilla-backend/            # Backend facade over CLI, used by frontend
|-- gruzilla-frontend/           # Web UI
|-- scenarios/                   # Scenario YAML files
|-- templates/                   # Template files used in scenario steps
|-- monitoring/                  # Monitoring stack config (Prometheus/Grafana etc.)
|-- logs/                        # Runtime logs (executor/backend when enabled)
|-- scripts/                     # Utility scripts
`-- README.md                    # Quick start and command examples
```

## 2) Runtime Components

### `gruzilla-executor` (runtime engine)

- Starts from `cmd/gruzilla-executor/main.go`.
- Loads scenario YAML into `internal/scenario.Scenario`.
- Exposes API:
  - `/api/v1/start`, `/stop`, `/update`, `/status`, `/reload`, `/reset_metrics`, `/shutdown`
  - `/metrics` (Prometheus scrape endpoint).
- Owns `internal/executor.Service`:
  - stores current config/status;
  - runs the 1-second tick scheduler (`runLoop`);
  - updates atomic counters and Prometheus metrics.

### `gruzilla-cli` (control plane)

- Starts from `cmd/gruzilla-cli/main.go`.
- Main command groups:
  - `run` - control active load;
  - `executors` - manage executor processes;
  - `scenarios` - CRUD for scenario YAML files;
  - `templates` - CRUD for template files.
- For `run*` commands it sends JSON POST requests to executor API.

### `gruzilla-backend` (frontend facade)

- Starts from `gruzilla-backend/main.go`.
- Exposes frontend-oriented API under `/api/v1/*`.
- Does not execute load itself:
  - builds CLI arguments from HTTP body;
  - executes `gruzilla-cli --output json ...`;
  - wraps CLI output into backend response contract.

### `gruzilla-frontend` (UI layer)

- Sends JSON requests to backend endpoints.
- Displays status/errors/CRUD results.

## 2.1) Component Interaction Diagram

```text
+---------------------+        POST /api/v1/*        +----------------------+
| gruzilla-frontend   | ---------------------------> | gruzilla-backend     |
| (Browser UI)        |                              | (HTTP facade)        |
+---------------------+                              +----------+-----------+
                                                                |
                                                                | exec process
                                                                v
                                                      +---------+------------+
                                                      | gruzilla-cli         |
                                                      | (control plane)      |
                                                      +----------+-----------+
                                                                 |
                                                                 | POST /api/v1/start/status/...
                                                                 v
                                                      +----------+-----------+
                                                      | gruzilla-executor    |
                                                      | (run engine + API)   |
                                                      +--+--------+-----+----+
                                                         |        |     |
                         load/reload scenarios/*.yml ----+        |     +--> execute steps --> REST/Kafka/DB/MQ
                                                                  |
                              render payloads templates/* --------+

 Prometheus/Grafana ---- scrape /metrics ------------------------> gruzilla-executor
```

## 3) Main Data Flows

### A) Start load from UI

```text
Frontend
  -> POST /api/v1/run/start (backend)
    -> backend builds CLI args
      -> gruzilla-cli run start --executor-url ...
        -> POST /api/v1/start (executor)
          -> Service.Start() + runLoop goroutine
            -> scenario steps execute (rest/kafka/db/mq)
```

### B) Status polling

```text
Frontend -> backend /api/v1/run/status
         -> CLI run status
         -> executor /api/v1/status
         -> Service.Status() snapshot
```

### C) Scenario/template CRUD

```text
Frontend -> backend /api/v1/scenarios/* or /templates/*
         -> CLI scenarios/templates commands
         -> filesystem operations in scenarios/ and templates/
```

## 4) Executor Internal Execution Model

On each tick (`internal/executor/service.go`):

1. Read current config (`percent`, `base_tps`, `ramp_up_seconds`, `ignore_load_schedule`, variables).
2. Calculate effective desired TPS (`effectiveTPSForScenario`: при наличии `scenario.LoadSchedule` и `IgnoreLoadSchedule == false` — по суточному расписанию в таймзоне сценария; иначе — `base_tps × percent`; затем ramp-up).
3. Apply adaptive cap to avoid uncontrolled overload.
4. Compute iteration count and enqueue that many jobs (non-blocking) for a **fixed worker pool** (`scenarioWorkerCount()`, derived from `GOMAXPROCS`), up to `scenarioMaxConcurrent` (4096) admitted jobs (semaphore + job queue).
5. Each job (worker):
   - creates per-iteration variables (`requestId`, `TransactionNumber`);
   - executes all scenario steps in order;
   - updates success/error/latency counters.
6. Recompute measured TPS from attempts delta.
7. Update status metrics + Prometheus gauges.

Concurrency controls:

- `sync.RWMutex` protects mutable status/config/scenario references.
- `atomic` counters store hot-path metric values.
- Semaphore + bounded `jobCh` cap total admitted load (queue + running) at 4096; long-lived workers avoid per-tick `go` storm.

## 5) Scenario Model (YAML)

Scenario is parsed into:

- `Scenario`:
  - `name`, `description`, `steps[]`
  - optional `load_schedule` (inline) **or** `load_schedule_profile` (path to YAML with `max_load`, `timezone`, `intervals`); not both
- `Step`:
  - common fields (`type`, `name`, `body`, `template`, `assert`, ...);
  - transport-specific fields:
    - `rest`: `method`, `url`, `headers`;
    - `kafka`: `topic`, `brokers`, `key`;
    - `db`: `db_dsn`, `db_query`;
    - `mq`: `queue`, `mq_action`, selectors, headers, credentials.

Validation strategy:

- lightweight structural validation at load time (`internal/scenario.Validate`);
- deeper runtime checks inside each step executor.

String fields in steps may use executor placeholders after variable interpolation: `{{__now:LAYOUT}}`, `{{__randDigits:N}}`, `{{__randHex:N}}` (`internal/executor/placeholders.go`).

## 6) Observability

- Optional **traffic log** to a file when executor is started with `--log-file`: step-level inbound/outbound messages (see `internal/executor/traffic_log.go`). Backend can enable a default log path via `config-backend.yml` (`executor_logs_enabled`, `executor_log_file`).
- Executor exports Prometheus gauges under `/metrics`.
- Core gauges:
  - attempts/success/errors totals,
  - current TPS,
  - target TPS,
  - last latency ms,
  - running flag.
- `Status` JSON includes `scenario_has_load_schedule` when the loaded scenario defines an active load schedule.
- Backend writes request-scoped logs with `request_id`.

## 7) Configuration Sources (Backend)

`gruzilla-backend` config precedence:

1. hardcoded defaults;
2. optional YAML config file (`config-backend.yml`);
3. environment variables (highest priority).

Important fields:

- CLI command/args/workdir;
- backend listen address;
- default executor URL;
- executor log-file policy (`executor_logs_enabled`, `executor_log_file`).

## 8) Operational Notes

- `reload` updates active scenario without process restart.
- `reset-metrics` is allowed only when run is stopped.
- API errors are returned inside JSON `status=error` with HTTP 200 (current contract).
- Process-management commands (`executors start/stop/restart/list`) are OS-aware.
