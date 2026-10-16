import { matchesQuery } from "@/lib/format"
import type { CatalogScenario, Session } from "@/lib/types"

export type ScenarioGroup = {
  resource: string
  displayName: string
  scenarios: CatalogScenario[]
}

export function groupScenarios(scenarios: CatalogScenario[]): ScenarioGroup[] {
  const groups: ScenarioGroup[] = []
  for (const scenario of scenarios) {
    const current = groups[groups.length - 1]
    if (groups.length > 0 && current.resource === scenario.resource) {
      current.scenarios.push(scenario)
    } else {
      groups.push({
        resource: scenario.resource,
        displayName: scenario.displayName,
        scenarios: [scenario],
      })
    }
  }
  return groups
}

export function matchingResources(
  scenarios: CatalogScenario[],
  query: string
): ScenarioGroup[] {
  const matched = groupScenarios(scenarios).flatMap((group) => {
    if (matchesQuery(query, [group.resource, group.displayName])) {
      return [group]
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
    if (scenariosForResource.length === 0) return []
    return [{ ...group, scenarios: scenariosForResource }]
  })
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
