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

export function sessionsForEnvironment(
  sessions: Session[],
  name: string
): Session[] {
  return sessions.filter(
    (session) => session.kind === "environment" && session.name === name
  )
}
