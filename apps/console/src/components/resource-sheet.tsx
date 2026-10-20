import { useEffect, useRef } from "react"
import { X } from "lucide-react"
import { AgentCopy } from "@/components/agent-copy"
import { ErrorNote } from "@/components/feedback"
import { Drawer } from "@/components/motion/drawer"
import { ResourceIcon } from "@/components/resource-icon"
import { StartActions } from "@/components/start-actions"
import { Button, LinkButton } from "@/components/ui/button"
import { resourceAgentBrief } from "@/lib/agent-brief"
import type { ScenarioGroup } from "@/lib/catalog"
import type { CatalogScenario, Session } from "@/lib/types"

export function ResourceSheet({
  resource,
  sessions,
  open,
  pending,
  errors,
  onOpenChange,
  onStart,
}: {
  resource: ScenarioGroup | null
  sessions: Session[] | null
  open: boolean
  pending: string | null
  errors: Record<string, string>
  onOpenChange: (open: boolean) => void
  onStart: (id: string) => void
}) {
  const closeRef = useRef<HTMLButtonElement>(null)
  useEffect(() => {
    if (open) closeRef.current?.focus()
  }, [open, resource?.resource])

  const scenarios = [...(resource?.scenarios ?? [])].sort((left, right) => {
    const byName = left.name.localeCompare(right.name)
    if (byName !== 0) return byName
    return left.id.localeCompare(right.id)
  })

  return (
    <Drawer
      open={open}
      onOpenChange={onOpenChange}
      side="right"
      ariaLabel={resource ? `${resource.displayName} scenarios` : "Scenarios"}
      className="w-[28rem] max-w-[92vw] bg-background"
    >
      {resource ? (
        <>
          <div className="flex items-start justify-between gap-3 border-b px-4 py-4">
            <div className="flex min-w-0 items-start gap-3">
              <ResourceIcon id={resource.resource} size="md" />
              <div className="min-w-0">
                <h2 className="text-lg font-medium">{resource.displayName}</h2>
                <p className="font-mono text-xs text-muted-foreground">
                  {resource.resource}
                </p>
                <p className="mt-2 text-sm text-muted-foreground">
                  Choose the scenario this sandbox loads.
                </p>
                <div className="mt-3">
                  <AgentCopy brief={resourceAgentBrief(resource, sessions)} />
                </div>
              </div>
            </div>
            <Button
              ref={closeRef}
              type="button"
              variant="ghost"
              size="icon"
              aria-label="Close scenarios"
              onClick={() => onOpenChange(false)}
            >
              <X className="size-4" />
            </Button>
          </div>
          <div className="min-h-0 flex-1 overflow-y-auto">
            <ul>
              {scenarios.map((scenario) => (
                <ScenarioChoice
                  key={scenario.id}
                  scenario={scenario}
                  pending={pending}
                  error={errors[`scenario:${scenario.id}`]}
                  onStart={() => onStart(scenario.id)}
                />
              ))}
            </ul>
          </div>
        </>
      ) : null}
    </Drawer>
  )
}

function ScenarioChoice({
  scenario,
  pending,
  error,
  onStart,
}: {
  scenario: CatalogScenario
  pending: string | null
  error?: string
  onStart: () => void
}) {
  return (
    <li className="border-b px-4 py-4">
      <div className="flex flex-col gap-3">
        <div className="min-w-0">
          <h3 className="font-medium">{scenario.name}</h3>
          <p className="mt-1 text-sm leading-6 text-muted-foreground">
            {scenario.description}
          </p>
          <p className="mt-1 font-mono text-xs text-muted-foreground">
            {scenario.id}
          </p>
        </div>
        <div className="flex items-center justify-end gap-2">
          <LinkButton to="/scenarios/$id" params={{ id: scenario.id }}>
            Explore
          </LinkButton>
          <StartActions
            pending={pending}
            actionKey={`scenario:${scenario.id}`}
            onStart={onStart}
          />
        </div>
        {error ? <ErrorNote compact>{error}</ErrorNote> : null}
      </div>
    </li>
  )
}
