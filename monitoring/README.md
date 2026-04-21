# Мониторинг gruzilla (Prometheus + Grafana)

## Метрики executor

После запуска `gruzilla-executor` по адресу **GET http://localhost:8081/metrics** отдаётся вывод в формате Prometheus:

- `gruzilla_attempts_total{scenario="..."}` — всего запущено попыток
- `gruzilla_success_total{scenario="..."}` — успешных
- `gruzilla_errors_total{scenario="..."}` — с ошибкой
- `gruzilla_current_tps{scenario="..."}` — фактический TPS за последний тик
- `gruzilla_target_tps{scenario="..."}` — целевой TPS (на executor уже учтены `percent`, ramp-up и при необходимости суточное `load_schedule` сценария; см. корневой `README.md`)
- `gruzilla_last_latency_ms{scenario="..."}` — латентность последнего запуска (мс)
- `gruzilla_running{scenario="..."}` — 1 если нагрузка идёт, 0 если остановлена

## Запуск стека

Из корня проекта:

```powershell
docker compose -f docker-compose.monitoring.yml up -d
```

Если executor запускается через CLI, можно использовать:

```powershell
go run ./cmd/gruzilla-cli executors start --scenario "C:\projects\load\gruzilla\scenarios\mq-topic1-request-reply.yml" --addr ":8081"
```

CLI попытается запустить локальный `gruzilla-executor(.exe)`, и только если бинарь не найден — сделает fallback на `go run`.

- **Prometheus:** http://localhost:9090  
- **Grafana:** http://localhost:3000 (логин/пароль: admin / admin)

В Grafana: **Connections → Data sources → Add data source → Prometheus** → URL: `http://prometheus:9090` → Save.

Пример запросов в Explore или дашборде:

- `gruzilla_current_tps` — текущий TPS
- `rate(gruzilla_success_total[1m])` — успехи в минуту (если метрики обновляются часто)
- `gruzilla_errors_total` — накопленные ошибки
