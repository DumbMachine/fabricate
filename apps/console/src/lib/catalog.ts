import { matchesQuery } from "@/lib/format"
import type { CatalogScenario, Session } from "@/lib/types"

export type ScenarioGroup = {
  resource: string
  displayName: string
  scenarios: CatalogScenario[]
}

export function matchingResources(
  scenarios: CatalogScenario[],
  query: string
): ScenarioGroup[] {
  const groups = new Map<string, ScenarioGroup>()
  for (const scenario of scenarios) {
    const existing = groups.get(scenario.resource)
    if (existing) {
      existing.scenarios.push(scenario)
      continue
    }
    groups.set(scenario.resource, {
      resource: scenario.resource,
      displayName: scenario.displayName,
      scenarios: [scenario],
    })
  }
  const matched: ScenarioGroup[] = []
  for (const group of groups.values()) {
    if (matchesQuery(query, [group.resource, group.displayName])) {
      matched.push(group)
      continue
    }
    const scenariosForResource = group.scenarios.filter((scenario) =>
      matchesQuery(query, [
        scenario.id,
        scenario.name,
        scenario.description,
        scenario.resourceVersion,
        ...scenario.providerHosts,
      ])
    )
    if (scenariosForResource.length === 0) continue
    matched.push({ ...group, scenarios: scenariosForResource })
  }
  return matched.sort(
    (left, right) =>
      left.displayName.localeCompare(right.displayName) ||
      left.resource.localeCompare(right.resource)
  )
}

export function sessionsForEnvironment(
  sessions: Session[],
  name: string
): Session[] {
  return sessions.filter(
    (session) => session.kind === "environment" && session.name === name
  )
}
