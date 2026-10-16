import { Link } from "@tanstack/react-router"
import { ResourceIcon } from "@/components/resource-icon"

export function ScenarioChip({
  resource,
  scenario,
}: {
  resource: string
  scenario: string
}) {
  return (
    <Link
      to="/scenarios/$id"
      params={{ id: scenario }}
      title={scenario}
      className="inline-flex max-w-full items-center gap-1.5 rounded-md border bg-card px-1.5 py-1 hover:bg-muted"
    >
      <ResourceIcon id={resource} />
      <span className="truncate font-mono text-xs">{scenario}</span>
    </Link>
  )
}

export function ScenarioChips({
  services,
}: {
  services: { resource: string; scenario: string; name?: string }[]
}) {
  if (services.length === 0) {
    return <span className="text-sm text-muted-foreground">No scenarios</span>
  }
  return (
    <div className="flex flex-wrap gap-1.5">
      {services.map((service) => (
        <ScenarioChip
          key={service.name ?? service.scenario}
          resource={service.resource}
          scenario={service.scenario}
        />
      ))}
    </div>
  )
}
