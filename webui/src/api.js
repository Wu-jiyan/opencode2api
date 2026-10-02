// Thin client for the management API. The CSRF token and the session-expiry
// callback are module state because every request shares them.
let csrfToken = "";
let onUnauthorized = null;

export function setCsrfToken(value) {
  csrfToken = value || "";
}

export function setUnauthorizedHandler(handler) {
  onUnauthorized = handler;
}

export async function api(path, options = {}) {
  const headers = { Accept: "application/json", ...(options.headers || {}) };
  if (options.body) headers["Content-Type"] = "application/json";
  const method = options.method || "GET";
  if (csrfToken && method !== "GET" && method !== "HEAD") {
    headers["X-CSRF-Token"] = csrfToken;
  }

  const response = await fetch(path, { ...options, headers });
  if (response.status === 401) {
    onUnauthorized?.();
    throw new Error("登录已失效，请重新登录");
  }
  if (response.status === 204) return null;

  const data = await response.json().catch(() => ({}));
  if (!response.ok) {
    throw new Error(data.error?.message || `HTTP ${response.status}`);
  }
  return data;
}

export function post(path, body) {
  return api(path, { method: "POST", body: JSON.stringify(body ?? {}) });
}

export function put(path, body) {
  return api(path, { method: "PUT", body: JSON.stringify(body ?? {}) });
}
