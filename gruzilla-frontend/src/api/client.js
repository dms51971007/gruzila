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
