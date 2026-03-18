# gruzilla-frontend

Initial frontend scaffold for Gruzilla.

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

## Implemented screens

- `Run` tab: start/update/status/reload/reset/stop calls to backend.
- `Scenarios` tab: basic CRUD calls.
- `Templates` tab: basic CRUD calls.

## API notes

- All calls are `POST`.
- Request header `X-Request-Id` is set for every request.
- Backend base URL is configurable in UI (default `http://localhost:8080`).
