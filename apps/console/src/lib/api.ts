import { publishSession } from "./publish-session.ts"
import type {
  Catalog,
  ConsoleConfig,
  RequestLogView,
  StartKind,
  AuthSession,
  ScenarioView,
  Session,
} from "./types.ts"

export async function getConfig(): Promise<ConsoleConfig> {
  return request<ConsoleConfig>("/api/config")
}

export async function getCatalog(): Promise<Catalog> {
  return request<Catalog>("/api/catalog")
}

export async function getScenario(id: string): Promise<ScenarioView> {
  return request<ScenarioView>(`/api/scenarios/${encodeURIComponent(id)}`)
}

export async function listSessions(): Promise<Session[]> {
  const sessions = await request<Session[]>("/api/sessions")
  return sessions.map(publishSession)
}

export async function getSession(id: string): Promise<Session> {
  return publishSession(
    await request<Session>(`/api/sessions/${encodeURIComponent(id)}`)
  )
}

export async function getSessionLogs(id: string): Promise<RequestLogView> {
  return request<RequestLogView>(`/api/sessions/${encodeURIComponent(id)}/logs`)
}

export async function startSandbox(
  kind: StartKind,
  id: string,
  idempotencyKey?: string
): Promise<Session> {
  return publishSession(
    await request<Session>("/api/sessions", {
      method: "POST",
      body: JSON.stringify({ kind, id, idempotencyKey }),
    })
  )
}

export async function closeSandbox(id: string): Promise<void> {
  await request<void>(`/api/sessions/${encodeURIComponent(id)}`, {
    method: "DELETE",
  })
}

export class ApiError extends Error {
  status: number
  constructor(message: string, status: number) {
    super(message)
    this.status = status
    this.name = "ApiError"
  }
}
export const getAuthSession = () => request<AuthSession>("/api/auth/session")
export const signIn = (token: string) =>
  request<AuthSession>("/api/auth/session", {
    method: "POST",
    body: JSON.stringify({ token }),
  })
export const signOut = () =>
  request<void>("/api/auth/session", { method: "DELETE" })

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, {
    credentials: "same-origin",
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
  let payload: unknown = null
  try {
    payload = text ? JSON.parse(text) : null
  } catch {
    if (response.ok) throw new Error("Fabricate returned an invalid response")
  }
  if (!response.ok) {
    if (response.status === 401 && path !== "/api/auth/session")
      window.dispatchEvent(new Event("fabricate:unauthorized"))
    throw new ApiError(
      errorMessage(
        payload,
        response.status === 401
          ? "Sign in to continue"
          : response.statusText || "Request failed"
      ),
      response.status
    )
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

export const getResourceContract = (resource: string) =>
  request<{ basePath: string; openapi: Record<string, unknown> }>(
    `/api/resources/${encodeURIComponent(resource)}/contract`
  )
