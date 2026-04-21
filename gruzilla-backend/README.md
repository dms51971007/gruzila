# gruzilla-backend

Backend-фасад для UI Gruzilla.

Принимает `POST /api/v1/*` от frontend и выполняет команды `gruzilla-cli`.
Backend не вызывает API executor напрямую.

## Запуск

Из `C:\projects\load\gruzilla`:

```powershell
go run ./gruzilla-backend
```

Адрес прослушивания по умолчанию: `:8080`.

Запуск с явным путём к конфигу:

```powershell
go run ./gruzilla-backend --config ".\config-backend.yml"
```

Файл конфига по умолчанию: `config-backend.yml` в текущей рабочей директории.

## Переменные окружения

- `GRUZILLA_BACKEND_ADDR` (default `:8080`)
- `GRUZILLA_BACKEND_CONFIG` (default `config-backend.yml`)
- `GRUZILLA_CLI_COMMAND` (default `go`)
- `GRUZILLA_CLI_ARGS` (default `run ./cmd/gruzilla-cli`)
- `GRUZILLA_CLI_WORKDIR` (default `.`)
- `GRUZILLA_CLI_TIMEOUT_SECONDS` (default `30`)
- `GRUZILLA_DEFAULT_EXECUTOR_URL` (default `http://localhost:8081`)

Переменные окружения имеют приоритет над значениями из `config-backend.yml`.

## Config file (`config-backend.yml`)

Помимо `addr` и блока `cli`, полезны:

- `cli.default_executor_url` — URL executor по умолчанию для UI/CLI.
- `cli.executor_logs_enabled` — при `true` и старте executor через API backend передаётся путь лог-файла.
- `cli.executor_log_file` — шаблон пути (например `logs/executor-{addr}.log`); подстановка `{addr}` из адреса listen executor.

### Как backend стартует executor

Backend вызывает `gruzilla-cli executors start/restart`. Если поле `bin` не передано в JSON-теле, CLI сам выбирает:

1. бинарь `gruzilla-executor` (`.exe` на Windows) в типовых путях;
2. fallback на `go run ./cmd/gruzilla-executor`, если бинарь не найден.

Чтобы принудительно задать способ запуска, передайте `bin` в body (`"go"` или полный путь к `.exe`/binary).

## JSON тела Run API

Эндпоинты `POST /api/v1/run/start` и `POST /api/v1/run/update` принимают JSON:

- `executor_url` (опционально)
- `percent`, `base_tps`, `ramp_up_seconds`
- `variables` (map, опционально) — для start
- `ignore_load_schedule` (опционально, boolean) — пробрасывается в `gruzilla-cli` как `--ignore-load-schedule` / `--ignore-load-schedule=false` (для `update` оба значения имеют смысл, чтобы явно включить или выключить игнор).

Ответ `run/status` проксирует вывод CLI; в данных статуса executor присутствует `scenario_has_load_schedule`, если в активном сценарии задано расписание нагрузки.

## Трассировка запросов

- Читает входящий заголовок `X-Request-Id`.
- Если заголовка нет — генерирует UUID-подобный идентификатор.
- Пробрасывает значение в CLI через `--request-id`.
- Возвращает `X-Request-Id` в заголовках ответа.
