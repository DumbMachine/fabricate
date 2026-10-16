import { Link } from "@tanstack/react-router"
import { ResourceIcon } from "@/components/resource-icon"

export function ScenarioChip({
  resource,
  scenario,
  plain = false,
}: {
  resource: string
  scenario: string
  plain?: boolean
}) {
  return (
    <Link
      to="/scenarios/$id"
      params={{ id: scenario }}
      title={scenario}
      className={
        plain
          ? "inline-flex max-w-full items-center gap-2 font-mono text-xs"
          : "inline-flex max-w-full items-center gap-1.5 rounded-md border bg-card px-1.5 py-1 hover:bg-muted"
      }
    >
      <ResourceIcon id={resource} />
      <span className="truncate">{scenario}</span>
    </Link>
  )
}

export function ScenarioChips({
  services,
  plain = false,
}: {
  services: { resource: string; scenario: string; name?: string }[]
  plain?: boolean
}) {
  if (services.length === 0) {
    return <span className="text-sm text-muted-foreground">No scenarios</span>
  }
  return (
    <div className={plain ? "flex flex-col gap-1" : "flex flex-wrap gap-1.5"}>
      {services.map((service) => (
        <ScenarioChip
          key={service.name ?? service.scenario}
          resource={service.resource}
          scenario={service.scenario}
          plain={plain}
        />
      ))}
    </div>
  )
}
