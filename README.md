
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

- `run start` — старт нагрузки (`--percent`, `--base-tps`, `--ramp-up-seconds`, `--var key=value`, `--ignore-load-schedule`)
- `run status` — текущий статус и метрики (в т.ч. `metrics.steps`: по каждому шагу сценария — `error_count`, `last_latency_ms`)
- `run update` — изменение TPS/percent/ramp без рестарта (опционально `--ignore-load-schedule` / `--ignore-load-schedule=false`)
- `run stop` — остановка нагрузки
- `run reload` — перечитать YAML сценария без перезапуска процесса
- `run reset-metrics` — обнулить метрики (только при остановленной нагрузке)

Примеры:

```powershell
go run ./cmd/gruzilla-cli run start --executor-url "http://localhost:8081" --percent 100 --base-tps 300 --ramp-up-seconds 180
go run ./cmd/gruzilla-cli run start --executor-url "http://localhost:8081" --percent 100 --base-tps 50 --ignore-load-schedule
go run ./cmd/gruzilla-cli run update --executor-url "http://localhost:8081" --base-tps 600
go run ./cmd/gruzilla-cli run status --executor-url "http://localhost:8081"
go run ./cmd/gruzilla-cli run reload --executor-url "http://localhost:8081"
go run ./cmd/gruzilla-cli run stop --executor-url "http://localhost:8081"
go run ./cmd/gruzilla-cli run reset-metrics --executor-url "http://localhost:8081"
```

## Встроенные переменные сценария

В каждом прогоне executor автоматически добавляет набор встроенных переменных.
Их можно использовать в `body`, `template`, `headers`, `mq_headers` и других строковых полях сценария.

- `requestId` — уникальный id на каждую итерацию сценария
- `TransactionNumber` — порядковый номер итерации (попытки)
- `executorId` — стабильный id текущего процесса `gruzilla-executor`
- `scenarioName` — имя активного сценария
- `scenarioPath` — путь к файлу активного сценария

Пример:

```yaml
mq_headers:
  X_ServiceID: "service-{executorId}"
  X_TransactionID: "plh1-{TransactionNumber}-{requestId}"
body: '{"RequestID":"{{requestId}}","scenario":"{{scenarioName}}"}'
```

Пользовательские переменные из `run start --var key=value` доступны вместе со встроенными.

### Плейсхолдеры времени и случайных значений (в строках шагов)

После подстановки `{{var}}` в полях вроде `body`, `headers`, `mq_headers` executor дополнительно разворачивает:

- `{{__now:LAYOUT}}` — `time.Now()` в формате Go [`time.Format`](https://pkg.go.dev/time#Time.Format) (например `{{__now:2006-01-02T15:04:05}}`); пустой `LAYOUT` даёт строку вида `0102150405`
- `{{__randDigits:N}}` — `N` десятичных цифр (crypto/rand)
- `{{__randHex:N}}` — `N` символов hex

### Шаблоны `.json.tmpl` (Go `text/template`)

Подстановка переменных — только в виде **`{{ .имя }}`** (например `{{ .requestId }}`, `{{ .payId }}`). Синтаксис вида `${name}` **не** обрабатывается движком шаблонов.

### MQ: корреляция `RequestId` и параллельные итерации

Для шагов `mq get` с `mq_selector: RequestId` брокер отфильтровывает сообщения по **JMS/STOMP-свойству** с тем же именем, что в заголовках исходящего `mq put`.

- Значение **`RequestId` в `mq_headers` должно быть уникальным на каждую итерацию** (обычно `{requestId}`), иначе при `base_tps > 0` несколько воркеров делят один селектор, ответы путаются, `mq get` зависает до таймаута, а `success_count` / `error_count` почти не растут.
- Профиль `scenarios/includes/mq-headers-executorid.yml` задаёт `RequestId: "{requestId}"` (а не общий `executorId` на весь процесс).

**TPS и «хвост» итераций:** `base_tps` задаёт число **новых** стартов сценария в секунду, а не число одновременно выполняющихся прогонов. Длинная цепочка MQ-шагов при малом TPS всё равно даёт **много параллельных** итераций — при необходимости снижайте `base_tps` или очищайте тестовые очереди от старых сообщений.

## Извлечение полей из JSON (`extract_var` / `extract_path`)

Универсально для любого шага с JSON-телом:

- `rest` — из тела HTTP-ответа (после проверки `assert.status`, если задан);
- `mq` с `mq_action: get` — из тела принятого сообщения (после успешного `assert` или сразу, если `assert` пуст).

```yaml
- type: rest
  method: GET
  url: "http://localhost:8090/api/v1/get-next-value?pool=plh_UC23_sublist&locked=false"
  extract_var: "omni_guid"
  extract_path: "values.omni_guid"
  assert:
    status: 200
```

Несколько полей за один шаг — map `extract` (имя переменной → путь):

```yaml
- type: rest
  method: GET
  url: "http://localhost:8090/api/v1/get-next-value?pool=plh&locked=false"
  extract:
    omni_guid: "values.omni_guid"
    rid: "rid"
  assert:
    status: 200
```

Можно совместить с одной парой `extract_var` / `extract_path` (применяется после всех ключей из `extract`).

Пример для `mq get`:

```yaml
- type: mq
  mq_action: get
  extract:
    omni_guid: "values.omni_guid"
```

Переменную из `extract_*` можно использовать в `body`/`template` следующих шагов той же итерации, например:

```yaml
- type: mq
  template: "mq-request-omni.json.tmpl"
```

В `.tmpl` поддерживается выбор одного JSON-варианта по кругу:

- если шаблон состоит из нескольких строк и каждая строка — валидный JSON,
- executor берет строку по индексу `(TransactionNumber-1) % N`.

Пример `templates/mq-request-omni.json.tmpl`:

```json
{"clientGuid":"{{ .omni_guid }}","RequestID":"{{ .requestId }}","var":1}
{"clientGuid":"{{ .omni_guid }}","RequestID":"{{ .requestId }}","var":2}
{"clientGuid":"{{ .omni_guid }}","RequestID":"{{ .requestId }}","var":3}
```

## Переиспользуемые профили шагов (include)

Чтобы не дублировать поля в шагах, можно вынести их в отдельные YAML-файлы и подключать по профилям:

```yaml
- type: rest
  rest_profile: "includes/rest-get-next-value.yml"
  rest_headers_profile: "includes/rest-headers-default.yml"

- type: mq
  mq_profile: "includes/mq-conn-ssl.yml"
  mq_headers_profile: "includes/mq-headers-default.yml"
```

Поддерживаемые поля:

- `rest_profile` — общие REST-поля (`method`, `url`, `body`, `template`, `extract_*`, `assert`, `headers`)
- `rest_headers_profile` — файл с `headers` (обертка `headers:` или прямая map)
- `mq_profile` — файл с общими MQ-параметрами (`mq_conn_name`, `mq_user`, TLS и т.п.)
- `mq_headers_profile` — файл с `mq_headers` (допускается как с корневым `mq_headers:`, так и прямой map)

Пути в профилях указываются относительно файла сценария (`scenarios/...`).

## Суточное расписание нагрузки (`load_schedule`)

В YAML сценария можно задать расписание по **локальному времени** (час суток 0–23, границы на `:00`):

- либо inline-блок `load_schedule:` с полями `max_load`, опционально `timezone`, `intervals` (ключ — час начала интервала, значение — процент от `max_load` до следующего ключа; сегменты могут переходить через полночь);
- либо `load_schedule_profile: "includes/....yml"` — в include-файле на корне те же поля (`max_load`, `timezone`, `intervals`), **без** вложенного `load_schedule:`.

Нельзя указывать одновременно `load_schedule` и `load_schedule_profile`. Пример профиля: `scenarios/includes/load-schedule-sbp.yml`, подключение — как в `scenarios/sbp-no-ssl.yml`.

**Целевой TPS** при активном расписании: `max_load × (процент интервала)/100 × (run percent)/100`, затем применяется обычный ramp-up из `RunConfig`.

Флаг **`ignore_load_schedule`** (CLI: `--ignore-load-schedule`, UI: чекбокс «игнорировать расписание» / «Без распис.» в строке executor): при значении `true` расписание **не** применяется, используется только `base_tps × percent/100` как без `load_schedule`.

В ответе `run status` у executor в `Status` есть поле `scenario_has_load_schedule` — в загруженном сценарии есть непустое расписание.

## Сценарий mq-topic1-request-reply

Семейство сценариев:

- `scenarios/mq-topic1-request-reply.yml`
- `scenarios/mq-topic1-request-reply-ssl.yml`
- `scenarios/mq-topic1-request-reply-no-ssl.yml`

Поток шагов:

1. (для no-ssl) `rest GET` получает `omni_guid`
2. `mq put` в `topic_1` (body через `template`)
3. `mq get` из `topic_2_` и проверка `success=true`

Выбор файла:

- `mq-topic1-request-reply-ssl.yml` — TLS/SSL к Artemis (`mq_tls: true`, truststore/keystore/cipher suites)
- `mq-topic1-request-reply-no-ssl.yml` — без TLS (`mq_tls: false`) + REST шаг к заглушке на `localhost:8090`
- `mq-topic1-request-reply.yml` — базовый вариант (можно держать как рабочую копию)

### Artemis через docker-compose

Из корня проекта:

```powershell
docker compose -f docker-compose.artemis.yml up -d
```

Web UI:

- `http://localhost:8161/console`
- логин/пароль: `artemis` / `artemis`

Требуемые сертификаты:

- `certs/broker-keystore.p12`
- `certs/truststore.p12`

Остановка:

```powershell
docker compose -f docker-compose.artemis.yml down
```

### Для no-ssl сценария: REST-заглушка

Сценарий `mq-topic1-request-reply-no-ssl.yml` ожидает endpoint:

- `GET http://localhost:8090/api/v1/get-next-value?pool=plh_UC23_sublist&locked=false`

Запуск заглушки (`kafka-db-sink`):

```powershell
cd C:\projects\load\kafka-db-sink
$env:HTTP_PORT=8090; .\gradlew.bat runValuePoolApi
```

## Сценарий `sbp-no-ssl`

Файл: `scenarios/sbp-no-ssl.yml` — цепочка **request-reply** через Artemis (STOMP) без TLS: несколько пар очередей `IN.RQ.*` / `OUT.RS.*`, шаблоны `templates/SBP_step1.json.tmpl` … `SBP_step4.json.tmpl`, финальный запрос — `templates/step5_SBP.json.tmpl`.

- В шаблонах для подстановки из переменных сценария используются **`{{ .requestId }}`**, **`{{ .payId }}`** (после `extract` на втором `mq get`), при необходимости **`{{ .numend }}`**, **`{{ .omni_guid }}`**.
- `payId` извлекается из ответа второй операции (`get-reply-step2`, путь `payment.orderId`).

### Заглушка очередей SBP (`kafka-db-sink`)

Репозиторий **`kafka-db-sink`** (рядом с `gruzilla`): слушает те же IN-очереди, что в сценарии, и шлёт ответы в парные OUT-очереди.

```powershell
cd C:\projects\load\kafka-db-sink
# Совпадайте порт/учётные данные с mq_conn_name / mq_user в includes/mq-conn-no-ssl.yml
$env:ARTEMIS_URL="tcp://localhost:61613"
.\gradlew.bat runSbpMock
```

Задержка ответа (мс): переменные окружения или `application.properties` в `kafka-db-sink`:

- `SBP_MOCK_DELAY_MS` / `sbp.mock.delay.ms` — по умолчанию для всех маршрутов;
- `SBP_MOCK_DELAY_BY_QUEUE` / `sbp.mock.delay.by.queue` — переопределение по имени listen-очереди, формат: `IN.RQ.TRNFOPERCHECK_01.00.00=200,...`

В логах пишутся входящие/исходящие сообщения (UTC, очередь, тело с усечением длинных JSON).

### Полезные последовательности команд

Запуск с нуля:

```powershell
go run ./cmd/gruzilla-cli executors start --scenario "C:\projects\load\gruzilla\scenarios\mq-topic1-request-reply.yml" --addr ":8081"
go run ./cmd/gruzilla-cli run start --executor-url "http://localhost:8081" --percent 100 --base-tps 100 --ramp-up-seconds 60
go run ./cmd/gruzilla-cli run status --executor-url "http://localhost:8081"
```

После изменения YAML:

```powershell
go run ./cmd/gruzilla-cli run reload --executor-url "http://localhost:8081"
go run ./cmd/gruzilla-cli run status --executor-url "http://localhost:8081"
```

Остановить и обнулить метрики:

```powershell
go run ./cmd/gruzilla-cli run stop --executor-url "http://localhost:8081"
go run ./cmd/gruzilla-cli run reset-metrics --executor-url "http://localhost:8081"
go run ./cmd/gruzilla-cli run status --executor-url "http://localhost:8081"
```

### SSL поля в сценарии

Для `mq-topic1-request-reply-ssl.yml` используются:

- `mq_tls: true`
- `mq_tls_truststore_path`
- `mq_tls_truststore_password`
- `mq_tls_keystore_path`
- `mq_tls_keystore_password`
- `mq_tls_cipher_suites` (например `TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256`)

Важно: сертификат брокера должен содержать hostname из `mq_conn_name` (SAN/CN), иначе TLS проверка имени завершится ошибкой.

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

## TCP ISO8583 по XML-спеке

Добавлен отдельный вид шага сценария: `type: tcp_iso8583_xml`.

Назначение: отправка ISO8583-сообщений по формату из внешнего XML-описания протокола (например `BPC8583POS.xml`).

Ключевые поля шага:

- `tcp_iso8583_spec_xml` — путь к XML-файлу спецификации;
- `tcp_iso8583_fields` — карта `номер_поля -> значение`;
- `tcp_length_prefix` — префикс длины TCP кадра (`4ascii`, `2be`, и т.д.).
- `tcp_pool_size` — размер пула TCP-соединений для шага (`0/1` — без пула, `>1` — переиспользование соединений между итерациями).

### TCP connection pool (`tcp_pool_size`)

- `tcp_pool_size: 0` или `1` — поведение как раньше: соединение открывается на шаг и закрывается после ответа.
- `tcp_pool_size: N` (`N > 1`) — используется пул до `N` соединений для данного `tcp_addr` (и TLS-параметров); после успешного шага соединение возвращается в пул.
- При ошибке чтения/записи проблемное соединение в пул не возвращается и закрывается.
- Если пул заполнен, «лишнее» соединение закрывается.

Минимальный пример:

```yaml
name: "tcp-iso8583-xml-bpc"
steps:
  - type: tcp_iso8583_xml
    tcp_addr: "127.0.0.1:9000"
    tcp_pool_size: 20
    tcp_length_prefix: "4ascii"
    tcp_iso8583_spec_xml: "c:\\projects\\BPC8583POS.xml"
    tcp_iso8583_fields:
      "0": "0200"
      "3": "000000"
      "11": "{{__randDigits:6}}"
```

Готовый полный пример в репозитории: `scenarios/tcp-iso8583-xml-bpc.yml`.

## Логи executor (трафик шагов)

Если процесс `gruzilla-executor` запущен с непустым **`--log-file`**, в этот файл дополнительно пишутся строки о трафике шагов (исходящие/входящие запросы и ответы, с временем и контекстом). Через UI/backend это настраивается в `config-backend.yml`: `cli.executor_logs_enabled` и `cli.executor_log_file` (в шаблоне имени файла можно использовать `{addr}`).

## Полезные ссылки

- Подробная схема проекта и потоков: `PROJECT_SCHEMA.md`
- Сценарий Artemis request-reply: `scenarios/mq-topic1-request-reply.yml`
- Сценарий SBP (несколько IN/OUT): `scenarios/sbp-no-ssl.yml`
- Backend facade для UI: `gruzilla-backend/README.md`
- Frontend UI: `gruzilla-frontend/README.md`
- Отклонения от ТЗ: `DEVIATIONS_FROM_TZ.md`

