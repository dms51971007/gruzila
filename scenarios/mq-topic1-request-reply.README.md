# mq-topic1-request-reply: запуск и команды gruzilla-cli

Этот документ описывает команды `gruzilla-cli` для сценария:

- `gruzilla/scenarios/mq-topic1-request-reply.yml`

Сценарий делает:

1. `mq put` в `topic_1`
2. `mq get` из `topic_2_` с проверкой `success=true`

---

## Быстрый старт (минимум)

Из каталога `gruzilla`:

```powershell
go run ./cmd/gruzilla-executor --scenario "C:\projects\load\gruzilla\scenarios\mq-topic1-request-reply.yml" --addr ":8081"
```

В другом терминале:

```powershell
go run ./cmd/gruzilla-cli run start --executor-url "http://localhost:8081" --percent 100 --base-tps 10
go run ./cmd/gruzilla-cli run status --executor-url "http://localhost:8081"
go run ./cmd/gruzilla-cli run stop --executor-url "http://localhost:8081"
```

---

## Глобальные флаги gruzilla-cli

Эти флаги можно добавлять к любой команде:

- `--executor-url` - URL executor API (по умолчанию: `http://localhost:8081`)
- `--output text|json` - формат вывода
- `--request-id` - request id для API-вызова CLI
- `--verbose` или `-v` - подробный вывод

Пример:

```powershell
go run ./cmd/gruzilla-cli --output json --executor-url "http://localhost:8081" run status
```

---

## Команды `gruzilla-cli run`

### 1) `run start`

Запускает генерацию нагрузки на уже работающем executor.

Флаги:

- `--percent` - коэффициент нагрузки в процентах (обычно `100`)
- `--base-tps` - базовый TPS сценария
- `--ramp-up-seconds` - линейный разгон TPS от 0 до целевого за N секунд
- `--var key=value` - переменные сценария (можно передавать несколько раз)

Примеры:

```powershell
go run ./cmd/gruzilla-cli run start --executor-url "http://localhost:8081" --percent 100 --base-tps 50
go run ./cmd/gruzilla-cli run start --executor-url "http://localhost:8081" --percent 100 --base-tps 2000 --ramp-up-seconds 300
go run ./cmd/gruzilla-cli run start --executor-url "http://localhost:8081" --percent 100 --base-tps 10 --var requestId=manual-1
```

### 2) `run status`

Показывает статус executor и метрики:

- `attempts_count`, `success_count`, `error_count`
- `last_latency_ms`
- `target_tps`, `current_tps`, `adaptive_tps`
- `last_error`

Пример:

```powershell
go run ./cmd/gruzilla-cli run status --executor-url "http://localhost:8081"
```

### 3) `run update`

Меняет параметры нагрузки без перезапуска run-сессии.

Флаги:

- `--percent`
- `--base-tps`
- `--ramp-up-seconds`

Примеры:

```powershell
go run ./cmd/gruzilla-cli run update --executor-url "http://localhost:8081" --base-tps 200
go run ./cmd/gruzilla-cli run update --executor-url "http://localhost:8081" --base-tps 2000 --ramp-up-seconds 180
```

### 4) `run stop`

Останавливает текущую нагрузку.

```powershell
go run ./cmd/gruzilla-cli run stop --executor-url "http://localhost:8081"
```

### 5) `run reset-metrics`

Сбрасывает счётчики и `last_error` (работает только когда run остановлен).

```powershell
go run ./cmd/gruzilla-cli run reset-metrics --executor-url "http://localhost:8081"
```

### 6) `run reload`

Перечитывает YAML сценария на executor без рестарта процесса.
Полезно после изменения `mq-topic1-request-reply.yml`.

```powershell
go run ./cmd/gruzilla-cli run reload --executor-url "http://localhost:8081"
```

---

## Команды `gruzilla-cli executors`

Эти команды управляют процессом `gruzilla-executor`.

### 1) `executors start`

Стартует новый процесс executor под нужный сценарий.

Флаги:

- `--scenario` - путь к `.yml` (обязательно)
- `--addr` - адрес API executor (по умолчанию `:8081`)
- `--bin` - `go` (по умолчанию) или путь к собранному бинарнику

Пример:

```powershell
go run ./cmd/gruzilla-cli executors start --scenario "C:\projects\load\gruzilla\scenarios\mq-topic1-request-reply.yml" --addr ":8081"
```

### 2) `executors restart`

Штатно завершает текущий executor через API и поднимает новый с тем же сценарием.

Флаги:

- `--scenario` - путь к `.yml` (обязательно)
- `--addr` - адрес запуска нового executor
- `--bin` - `go` или путь к бинарнику
- `--executor-url` - URL текущего executor для shutdown

Пример:

```powershell
go run ./cmd/gruzilla-cli executors restart --scenario "C:\projects\load\gruzilla\scenarios\mq-topic1-request-reply.yml" --addr ":8081" --executor-url "http://localhost:8081"
```

---

## Полезные последовательности для этого сценария

### Запуск с нуля

```powershell
go run ./cmd/gruzilla-cli executors start --scenario "C:\projects\load\gruzilla\scenarios\mq-topic1-request-reply.yml" --addr ":8081"
go run ./cmd/gruzilla-cli run start --executor-url "http://localhost:8081" --percent 100 --base-tps 100 --ramp-up-seconds 60
go run ./cmd/gruzilla-cli run status --executor-url "http://localhost:8081"
```

### Изменил YAML сценария

```powershell
go run ./cmd/gruzilla-cli run reload --executor-url "http://localhost:8081"
go run ./cmd/gruzilla-cli run status --executor-url "http://localhost:8081"
```

### Чисто остановить и обнулить метрики

```powershell
go run ./cmd/gruzilla-cli run stop --executor-url "http://localhost:8081"
go run ./cmd/gruzilla-cli run reset-metrics --executor-url "http://localhost:8081"
go run ./cmd/gruzilla-cli run status --executor-url "http://localhost:8081"
```

