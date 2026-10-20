import { sessionsForEnvironment, type ScenarioGroup } from "@/lib/catalog"
import type {
  CatalogEnvironment,
  SandboxKind,
  ScenarioView,
  Session,
  SessionService,
} from "@/lib/types"

export type AgentBrief = {
  details: string
  secrets: string
}

const handoff =
  "Do not use production accounts. Do not commit these credentials or paste them into a pull request."

export function agentBriefText(
  brief: AgentBrief,
  includeSecrets: boolean
): string {
  if (!includeSecrets || brief.secrets === "") return brief.details
  return `${brief.details}\n\n${brief.secrets}`
}

export function scenarioAgentBrief(
  view: ScenarioView,
  sessions: Session[] | null
): AgentBrief {
  const running = (sessions ?? []).filter((session) =>
    session.services.some((service) => service.scenario === view.id)
  )
  return {
    details: join([
      "Use this Fabricate scenario for provider calls in this task.",
      handoff,
      "",
      `Scenario: ${view.name}`,
      `Id: ${view.id}`,
      `Resource: ${view.displayName} (${view.resource})`,
      `Description: ${view.description}`,
      `Version: ${view.resourceVersion}`,
      `Production hostnames: ${hosts(view.providerHosts)}`,
      "",
      "Seed:",
      ...seedLines(view),
      "",
      startRequest("scenario", view.id),
      "",
      runningDetails(sessions, running),
    ]),
    secrets: secretsBlock(running),
  }
}

export function resourceAgentBrief(
  resource: ScenarioGroup,
  sessions: Session[] | null
): AgentBrief {
  const running = (sessions ?? []).filter((session) =>
    session.services.some((service) => service.resource === resource.resource)
  )
  const scenarios = [...resource.scenarios].sort((left, right) => {
    const byName = left.name.localeCompare(right.name)
    if (byName !== 0) return byName
    return left.id.localeCompare(right.id)
  })
  return {
    details: join([
      "Use this Fabricate resource for provider calls in this task.",
      handoff,
      "",
      `Resource: ${resource.displayName} (${resource.resource})`,
      "",
      "Scenarios:",
      ...scenarios.flatMap((scenario) => [
        `- ${scenario.name}`,
        `  Id: ${scenario.id}`,
        `  Description: ${scenario.description}`,
        `  Version: ${scenario.resourceVersion}`,
        `  Production hostnames: ${hosts(scenario.providerHosts)}`,
        `  ${startRequest("scenario", scenario.id)}`,
      ]),
      "",
      runningDetails(sessions, running),
    ]),
    secrets: secretsBlock(running),
  }
}

export function environmentAgentBrief(
  environment: CatalogEnvironment,
  sessions: Session[] | null
): AgentBrief {
  const running =
    sessions === null ? [] : sessionsForEnvironment(sessions, environment.name)
  return {
    details: join([
      "Use this Fabricate environment for provider calls in this task.",
      handoff,
      "",
      `Environment: ${environment.label}`,
      `Name: ${environment.name}`,
      "",
      "Services:",
      ...environment.services.flatMap((service) => [
        `- ${service.displayName} (${service.name})`,
        `  Resource: ${service.resource}`,
        `  Scenario: ${service.scenario}`,
        `  Production hostnames: ${hosts(service.providerHosts)}`,
      ]),
      "",
      startRequest("environment", environment.name),
      "",
      runningDetails(sessions, running),
    ]),
    secrets: secretsBlock(running),
  }
}

export function sandboxAgentBrief(session: Session): AgentBrief {
  return {
    details: join([
      "A Fabricate sandbox is running. Use it for provider calls in this task.",
      handoff,
      "",
      runningDetails([session], [session]),
    ]),
    secrets: secretsBlock([session]),
  }
}

function startRequest(kind: SandboxKind, id: string): string {
  return `Start: POST ${apiPath("/api/sessions")} {"kind":"${kind}","id":"${id}"}`
}

function runningDetails(known: Session[] | null, sessions: Session[]): string {
  if (known === null) return "Running sandboxes are still loading."
  if (sessions.length === 0) {
    return "Nothing is running yet. Start it with the request above, then call the service URL in the response."
  }
  return join([
    "Running:",
    ...sessions.flatMap((session) => sessionDetails(session)),
  ])
}

function sessionDetails(session: Session): string[] {
  const lines = [
    `- Session ${session.id} (${session.kind}, ${session.label})`,
    session.proxy
      ? "  Mode: proxy. Keep each provider hostname. The proxy replaces Authorization."
      : "  Mode: direct. Send the bearer token to the service base URL.",
  ]
  for (const service of session.services) {
    lines.push(...serviceDetails(session, service))
  }
  if (session.requestLog) {
    lines.push(`  Request log file: ${session.requestLog}`)
  }
  if (session.logs) {
    lines.push(`  Redacted request log: GET ${session.logs}`)
  }
  if (session.anchored) {
    lines.push("  Held by fab run. It stays up until that command exits.")
  } else if (session.logs) {
    lines.push(`  Close: DELETE ${session.logs.replace(/\/logs$/, "")}`)
  }
  return lines
}

function serviceDetails(session: Session, service: SessionService): string[] {
  const lines = [
    `  Service ${service.name} (${service.displayName})`,
    `  Scenario: ${service.scenario}`,
    `  Base URL: ${service.url}`,
    `  URL name: ${service.urlVariable}`,
    `  Secret name: ${service.tokenVariable}`,
    `  Production hostnames: ${hosts(service.providerHosts)}`,
  ]
  if (session.proxy) {
    lines.push(
      "  Call the production hostname through the proxy. Do not send the bearer token on that host."
    )
  } else {
    lines.push(
      `  Call the base URL and send Authorization: Bearer <${service.tokenVariable}>.`
    )
  }
  return lines
}

function secretsBlock(sessions: Session[]): string {
  const blocks = sessions
    .map((session) => secretLines(session))
    .filter((lines) => lines.length > 0)
  if (blocks.length === 0) return ""
  return join(["Secrets:", ...blocks.flat()])
}

function secretLines(session: Session): string[] {
  const lines: string[] = []
  const seen = new Set<string>()
  for (const service of session.services) {
    if (service.token === "") continue
    lines.push(`  ${service.tokenVariable}=${service.token}`)
    seen.add(service.tokenVariable)
  }
  for (const variable of session.variables) {
    if (seen.has(variable.name) || !secretVariable(variable.name)) continue
    lines.push(`  ${variable.name}=${variable.value}`)
    seen.add(variable.name)
  }
  if (lines.length === 0) return []
  return [`Session ${session.id}`, ...lines]
}

function secretVariable(name: string): boolean {
  const upper = name.toUpperCase()
  if (upper.endsWith("_TOKEN")) return true
  switch (upper) {
    case "HTTP_PROXY":
    case "HTTPS_PROXY":
    case "SSL_CERT_FILE":
    case "NODE_EXTRA_CA_CERTS":
    case "REQUESTS_CA_BUNDLE":
    case "GIT_SSL_CAINFO":
      return true
    default:
      return false
  }
}

function seedLines(view: ScenarioView): string[] {
  const lines: string[] = []
  for (const scalar of view.scalars) {
    lines.push(`- ${scalar.key}: ${scalar.value}`)
  }
  for (const collection of view.collections) {
    lines.push(`- ${collection.key}: ${collection.total}`)
    if (collection.columns.length === 0 || collection.rows.length === 0)
      continue
    lines.push(`  ${collection.columns.join(" | ")}`)
    for (const row of collection.rows) {
      lines.push(
        `  ${collection.columns.map((column) => row[column] ?? "").join(" | ")}`
      )
    }
    if (collection.shown < collection.total) {
      lines.push(`  … ${collection.total - collection.shown} more rows`)
    }
  }
  if (lines.length === 0) lines.push("- Empty seed")
  return lines
}

function hosts(values: string[]): string {
  return values.length > 0 ? values.join(", ") : "none"
}

function apiPath(path: string): string {
  if (typeof window === "undefined") return path
  return `${window.location.origin}${path}`
}

function join(lines: string[]): string {
  return lines
    .join("\n")
    .replace(/\n{3,}/g, "\n\n")
    .trim()
}
