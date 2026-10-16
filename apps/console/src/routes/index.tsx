import { Link, createFileRoute } from "@tanstack/react-router"
import { useMemo, useState } from "react"
import { cn } from "cn"
import { ErrorNote, LoadingLine } from "@/components/feedback"
import {
  Card,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/motion/card"
import { ResourceIcon } from "@/components/resource-icon"
import { ResourceSheet } from "@/components/resource-sheet"
import { ScenarioChips } from "@/components/scenario-chip"
import { ServiceURLs } from "@/components/service-urls"
import { StatusPill } from "@/components/status-pill"
import { StatefulButton } from "@/components/ui/button"
import {
  ColumnHeading,
  ConsoleFrame,
  DataTable,
} from "@/components/console-frame"
import { matchingResources } from "@/lib/catalog"
import { formatTime, matchesQuery } from "@/lib/format"
import { useCatalog, useSandboxActions, useSessions } from "@/lib/use-console"
import type { Session } from "@/lib/types"

export const Route = createFileRoute("/")({ component: SandboxesPage })

function SandboxesPage() {
  const { catalog, error: catalogError } = useCatalog()
  const [sessions, setSessions] = useSessions()
  const { pending, errors, start, close } = useSandboxActions()
  const [query, setQuery] = useState("")
  const [resourceId, setResourceId] = useState<string | null>(null)
  const visibleSessions = useMemo(() => {
    const loaded = sessions ?? []
    return loaded.filter((session) =>
      matchesQuery(query, [
        session.name,
        session.label,
        session.kind,
        session.id,
        ...session.services.flatMap((service) => [
          service.name,
          service.resource,
          service.displayName,
          service.scenario,
          service.url,
        ]),
      ])
    )
  }, [sessions, query])
  const resources = useMemo(
    () => matchingResources(catalog?.scenarios ?? [], query),
    [catalog, query]
  )
  const selected =
    resources.find((resource) => resource.resource === resourceId) ?? null

  async function onStart(id: string) {
    await start("scenario", id, "sandbox")
  }

  async function onClose(id: string) {
    const ok = await close(id)
    if (ok) {
      setSessions((current) =>
        (current ?? []).filter((session) => session.id !== id)
      )
    }
  }

  return (
    <ConsoleFrame
      crumbs={[{ label: "Sandboxes" }]}
      heading="Sandboxes"
      description="A running sandbox shows the scenario that is active. Open a resource to choose a scenario, or start every service in an environment together."
      search={{
        value: query,
        onChange: setQuery,
        label: "Search sandboxes and resources",
      }}
    >
      <section className="flex flex-col gap-3">
        <h2 className="text-lg font-medium">Running</h2>
        {sessions === null ? (
          <LoadingLine>Loading sandboxes…</LoadingLine>
        ) : visibleSessions.length === 0 ? (
          <Card variant="muted" size="compact">
            <CardHeader>
              <CardTitle as="h3">Nothing is running</CardTitle>
              <CardDescription>
                Open a resource below, or open{" "}
                <Link
                  to="/environments"
                  className="underline decoration-muted-foreground/50 underline-offset-2"
                >
                  Environments
                </Link>{" "}
                to bring up a whole set of services with one button.
              </CardDescription>
            </CardHeader>
          </Card>
        ) : (
          <DataTable className="min-w-[980px]">
            <thead>
              <tr>
                <ColumnHeading>Scenario</ColumnHeading>
                <ColumnHeading>UID</ColumnHeading>
                <ColumnHeading>URL</ColumnHeading>
                <ColumnHeading>Status</ColumnHeading>
                <ColumnHeading>Started</ColumnHeading>
                <ColumnHeading> </ColumnHeading>
              </tr>
            </thead>
            <tbody>
              {visibleSessions.map((session) => (
                <RunningRow
                  key={session.id}
                  session={session}
                  pending={pending}
                  error={errors[`close:${session.id}`]}
                  onClose={() => void onClose(session.id)}
                />
              ))}
            </tbody>
          </DataTable>
        )}
      </section>

      <section className="flex flex-col gap-3">
        <h2 className="text-lg font-medium">
          Resources{catalog ? ` (${resources.length})` : ""}
        </h2>
        {catalogError ? <ErrorNote>{catalogError}</ErrorNote> : null}
        {catalog === null && !catalogError ? (
          <LoadingLine>Loading resources…</LoadingLine>
        ) : null}
        {catalog !== null && resources.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            No resources match that search.
          </p>
        ) : null}
        <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
          {resources.map((resource) => {
            const selectedResource = resource.resource === resourceId
            const count = scenarioCount(resource.scenarios.length)
            return (
              <button
                key={resource.resource}
                type="button"
                aria-haspopup="dialog"
                aria-expanded={selectedResource}
                onClick={() => setResourceId(resource.resource)}
                className={cn(
                  "flex items-center gap-3 rounded-2xl border bg-background px-4 py-4 text-left hover:bg-muted/40 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground",
                  selectedResource ? "border-foreground" : "border-border"
                )}
              >
                <ResourceIcon id={resource.resource} size="md" />
                <span className="min-w-0">
                  <span className="block text-[15px] font-semibold">
                    {resource.displayName}
                  </span>
                  <span className="mt-0.5 block text-sm text-muted-foreground">
                    {count}
                  </span>
                </span>
              </button>
            )
          })}
        </div>
      </section>
      <ResourceSheet
        resource={selected}
        open={selected !== null}
        pending={pending}
        errors={errors}
        onOpenChange={(next) => {
          if (!next) setResourceId(null)
        }}
        onStart={(id) => void onStart(id)}
      />
    </ConsoleFrame>
  )
}

function scenarioCount(count: number): string {
  return count === 1 ? "1 scenario" : `${count} scenarios`
}

function RunningRow({
  session,
  pending,
  error,
  onClose,
}: {
  session: Session
  pending: string | null
  error?: string
  onClose: () => void
}) {
  return (
    <tr className="border-t align-top">
      <td className="px-3 py-3">
        <ScenarioChips services={session.services} plain />
        {error ? (
          <div className="mt-1">
            <ErrorNote compact>{error}</ErrorNote>
          </div>
        ) : null}
      </td>
      <td className="px-3 py-3">
        <Link
          to="/sandboxes/$id"
          params={{ id: session.id }}
          className="font-mono text-xs underline decoration-muted-foreground/50 underline-offset-2"
        >
          {session.id}
        </Link>
      </td>
      <td className="px-3 py-3">
        <ServiceURLs services={session.services} />
      </td>
      <td className="px-3 py-3">
        {session.anchored ? (
          <StatusPill tone="held">Held by fab run</StatusPill>
        ) : (
          <StatusPill tone="ready">Ready</StatusPill>
        )}
      </td>
      <td className="px-3 py-3 text-muted-foreground">
        {formatTime(session.startedAt)}
      </td>
      <td className="px-3 py-3 text-right">
        {session.anchored ? null : (
          <StatefulButton
            variant="outline"
            size="sm"
            state={pending === `close:${session.id}` ? "loading" : "idle"}
            loadingText="Closing…"
            disabled={pending !== null && pending !== `close:${session.id}`}
            onClick={onClose}
          >
            Close
          </StatefulButton>
        )}
      </td>
    </tr>
  )
}
