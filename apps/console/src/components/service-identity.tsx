import { Link } from "@tanstack/react-router"
import { ResourceIcon } from "@/components/resource-icon"

export function ServiceIdentity({
  resource,
  displayName,
  scenario,
  detail,
}: {
  resource: string
  displayName: string
  scenario: string
  detail?: string
}) {
  return (
    <div className="flex items-center gap-2">
      <ResourceIcon id={resource} />
      <div className="min-w-0">
        <p className="font-medium">{displayName}</p>
        {detail ? (
          <p className="font-mono text-xs text-muted-foreground">{detail}</p>
        ) : null}
        <Link
          to="/scenarios/$id"
          params={{ id: scenario }}
          className="font-mono text-xs break-all underline decoration-muted-foreground/50 underline-offset-2"
        >
          {scenario}
        </Link>
      </div>
    </div>
  )
}
