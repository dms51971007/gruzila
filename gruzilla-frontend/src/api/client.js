const DEFAULT_BASE_URL = "http://localhost:8080";

function generateRequestId() {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return crypto.randomUUID();
  }
  return `req-${Date.now()}-${Math.floor(Math.random() * 1e9)}`;
}

export async function postApi(path, body = {}, options = {}) {
  const baseUrl = options.baseUrl || DEFAULT_BASE_URL;
  const requestId = options.requestId || generateRequestId();

  const response = await fetch(`${baseUrl}${path}`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "X-Request-Id": requestId,
    },
    body: JSON.stringify(body),
  });

  const text = await response.text();
  let payload = null;
  try {
    payload = text ? JSON.parse(text) : null;
  } catch {
    payload = { status: "error", error: `Invalid JSON response: ${text}` };
  }

  return {
    ok: response.ok,
    status: response.status,
    requestId,
    payload,
  };
}

export function extractCliData(payload) {
  if (!payload || typeof payload !== "object") return null;
  if (payload.status !== "success") return null;
  const outer = payload.data;
  if (outer && typeof outer === "object" && "status" in outer) {
    if (outer.status !== "success") return null;
    return outer.data;
  }
  return outer;
}

/**
 * Текст файла из ответа templates/read или scenarios/read.
 * Обычно приходит в `data.stdout`. Если содержимое файла — валидный JSON, backend
 * парсит весь stdout как JSON и кладёт объект в `data` без поля `stdout` — тогда
 * собираем строку обратно для редактора.
 */
/** Сортировка путей по имени файла (без каталога), без учёта регистра. */
export function sortPathsByFileName(paths) {
  if (!Array.isArray(paths)) return [];
  const base = (p) => {
    const s = String(p || "").replace(/\\/g, "/");
    const i = s.lastIndexOf("/");
    return i >= 0 ? s.slice(i + 1) : s;
  };
  return [...paths].sort((a, b) => base(a).localeCompare(base(b), undefined, { sensitivity: "base" }));
}

export function extractReadFileStdout(data) {
  if (data == null) return undefined;
  if (typeof data === "string") return data;
  if (typeof data === "number" || typeof data === "boolean") return String(data);
  if (typeof data !== "object") return undefined;
  if (typeof data.stdout === "string") return data.stdout;
  try {
    return JSON.stringify(data, null, 2);
  } catch {
    return undefined;
  }
}
