# gruzilla-frontend

Web UI for Gruzilla operators (React, MUI, Vite).

## Stack

- React
- MUI
- Vite

## Run

```powershell
cd C:\projects\load\gruzilla\gruzilla-frontend
npm install
npm run dev
```

Default dev URL: `http://localhost:5173`.

Production build: `npm run build` (output in `dist/`).

## Implemented screens

- **`Run` tab** — управление нагрузкой через backend: Start / Update / Status / Reload / Reset metrics / Stop; параметры percent, base TPS, ramp; список executor. Если в сценарии есть расписание, показывается чекбокс с подписью вроде **«Игнорировать расписание сценария (только Base TPS)»** — он задаёт `ignore_load_schedule` в теле `run/start` и `run/update` (одна кнопка Start; режим расписания только через чекбокс).
- **`Executors` tab** (`ExecutorsPanel`) — таблица процессов: для строк с `scenario_has_load_schedule` в статусе показываются чип расписания, те же числовые поля и чекбокс **«Без распис.»**; кнопка Play запускает или обновляет прогон с параметрами строки (включая `ignore_load_schedule`).
- **`Scenarios` tab** — CRUD YAML сценариев.
- **`Templates` tab** — CRUD шаблонов.

## API notes

- Все запросы к backend — **`POST`**.
- Заголовок **`X-Request-Id`** выставляется на каждый запрос.
- Базовый URL backend настраивается в UI (по умолчанию `http://localhost:8080`).
- Тела `run/start` и `run/update` могут содержать `ignore_load_schedule` (boolean), см. `gruzilla-backend/README.md`.
