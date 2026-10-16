import type { Catalog, SandboxKind, ScenarioView, Session } from "@/lib/types"

export async function getCatalog(): Promise<Catalog> {
  return request<Catalog>("/api/catalog")
}

export async function getScenario(id: string): Promise<ScenarioView> {
  return request<ScenarioView>(`/api/scenarios/${encodeURIComponent(id)}`)
}

export async function listSessions(): Promise<Session[]> {
  return request<Session[]>("/api/sessions")
}

export async function getSession(id: string): Promise<Session> {
  return request<Session>(`/api/sessions/${encodeURIComponent(id)}`)
}

export async function startSandbox(
  kind: SandboxKind,
  id: string,
  proxy: boolean
): Promise<Session> {
  return request<Session>("/api/sessions", {
    method: "POST",
    body: JSON.stringify({ kind, id, proxy }),
  })
}

export async function closeSandbox(id: string): Promise<void> {
  await request<void>(`/api/sessions/${encodeURIComponent(id)}`, {
    method: "DELETE",
  })
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, {
    ...init,
    headers: {
      accept: "application/json",
      ...(init?.body ? { "content-type": "application/json" } : {}),
      ...init?.headers,
    },
  })
  if (response.status === 204) {
    return undefined as T
  }
  const text = await response.text()
  const payload: unknown = text ? JSON.parse(text) : null
  if (!response.ok) {
    throw new Error(errorMessage(payload, response.statusText))
  }
  return payload as T
}

function errorMessage(payload: unknown, fallback: string): string {
  if (
    typeof payload === "object" &&
    payload !== null &&
    "error" in payload &&
    typeof payload.error === "string"
  ) {
    return payload.error
  }
  return fallback
}
