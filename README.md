
# Gruzilla

`gruzilla` — нагрузочный раннер сценариев из YAML с HTTP API (`gruzilla-executor`) и CLI (`gruzilla-cli`).

## Быстрый старт

Из корня `gruzilla`:

```powershell
# 1) Запустить executor с конкретным сценарием
go run ./cmd/gruzilla-executor --scenario "C:\projects\load\gruzilla\scenarios\mq-topic1-request-reply.yml" --addr ":8081"
```

В отдельном терминале:

```powershell
# 2) Запустить нагрузку
go run ./cmd/gruzilla-cli run start --executor-url "http://localhost:8081" --percent 100 --base-tps 100 --ramp-up-seconds 60

# 3) Проверить статус
go run ./cmd/gruzilla-cli run status --executor-url "http://localhost:8081"

# 4) Остановить
go run ./cmd/gruzilla-cli run stop --executor-url "http://localhost:8081"
```

## Глобальные флаги `gruzilla-cli`

- `--executor-url` — URL executor API (по умолчанию `http://localhost:8081`)
- `--output text|json` — формат вывода
- `--request-id` — request id для вызова API
- `--verbose` (`-v`) — подробный вывод

Пример:

```powershell
go run ./cmd/gruzilla-cli --output json --executor-url "http://localhost:8081" run status
```

## Команды `gruzilla-cli run`

Управление нагрузкой на уже запущенном executor:

- `run start` — старт нагрузки (`--percent`, `--base-tps`, `--ramp-up-seconds`, `--var key=value`)
- `run status` — текущий статус и метрики
- `run update` — изменение TPS/percent/ramp без рестарта
- `run stop` — остановка нагрузки
- `run reload` — перечитать YAML сценария без перезапуска процесса
- `run reset-metrics` — обнулить метрики (только при остановленной нагрузке)

Примеры:

```powershell
go run ./cmd/gruzilla-cli run start --executor-url "http://localhost:8081" --percent 100 --base-tps 300 --ramp-up-seconds 180
go run ./cmd/gruzilla-cli run update --executor-url "http://localhost:8081" --base-tps 600
go run ./cmd/gruzilla-cli run status --executor-url "http://localhost:8081"
go run ./cmd/gruzilla-cli run reload --executor-url "http://localhost:8081"
go run ./cmd/gruzilla-cli run stop --executor-url "http://localhost:8081"
go run ./cmd/gruzilla-cli run reset-metrics --executor-url "http://localhost:8081"
```

## Команды `gruzilla-cli executors`

Управление процессом `gruzilla-executor`:

- `executors start` — старт нового процесса executor
- `executors restart` — мягкий перезапуск (shutdown API + новый start)

Примеры:

```powershell
go run ./cmd/gruzilla-cli executors start --scenario "C:\projects\load\gruzilla\scenarios\mq-topic1-request-reply.yml" --addr ":8081"
go run ./cmd/gruzilla-cli executors restart --scenario "C:\projects\load\gruzilla\scenarios\mq-topic1-request-reply.yml" --addr ":8081" --executor-url "http://localhost:8081"
```

## Команды `gruzilla-cli scenarios` (CRUD YAML)

Управление YAML-сценариями на диске:

- `scenarios list` — список `.yml/.yaml` в директории
- `scenarios read` — прочитать файл сценария
- `scenarios create` — создать файл (`--content` / `--from-file` / автогенерация)
- `scenarios update` — обновить существующий файл (`--content` или `--from-file`)
- `scenarios delete` — удалить файл (требует `--yes`)

Примеры:

```powershell
go run ./cmd/gruzilla-cli scenarios list --dir "scenarios"
go run ./cmd/gruzilla-cli scenarios read --path "mq-topic1-request-reply.yml" --dir "scenarios"
go run ./cmd/gruzilla-cli scenarios create --path "new-scenario.yml" --dir "scenarios" --name "new-scenario" --description "draft"
go run ./cmd/gruzilla-cli scenarios update --path "new-scenario.yml" --dir "scenarios" --from-file "C:\temp\updated.yml"
go run ./cmd/gruzilla-cli scenarios delete --path "new-scenario.yml" --dir "scenarios" --yes
```

## Команды `gruzilla-cli templates` (CRUD)

Управление файлами шаблонов на диске:

- `templates list` — список файлов в директории шаблонов
- `templates read` — прочитать файл шаблона
- `templates create` — создать файл (`--content` / `--from-file` / дефолтный шаблон)
- `templates update` — обновить существующий файл (`--content` или `--from-file`)
- `templates delete` — удалить файл (требует `--yes`)

Примеры:

```powershell
go run ./cmd/gruzilla-cli templates list --dir "templates"
go run ./cmd/gruzilla-cli templates read --path "example.json.tmpl" --dir "templates"
go run ./cmd/gruzilla-cli templates create --path "new.json.tmpl" --dir "templates" --content "{\"requestId\":\"{{requestId}}\"}"
go run ./cmd/gruzilla-cli templates update --path "new.json.tmpl" --dir "templates" --from-file "C:\temp\new-template.tmpl"
go run ./cmd/gruzilla-cli templates delete --path "new.json.tmpl" --dir "templates" --yes
```

## Полезные ссылки

- Сценарий Artemis request-reply: `scenarios/mq-topic1-request-reply.yml`
- Подробная инструкция по этому сценарию: `scenarios/mq-topic1-request-reply.README.md`
- Backend facade для UI: `gruzilla-backend/README.md`
- Frontend UI: `gruzilla-frontend/README.md`
- Отклонения от ТЗ: `DEVIATIONS_FROM_TZ.md`

