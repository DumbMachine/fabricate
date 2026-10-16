import { Link, createFileRoute } from "@tanstack/react-router"
import { useMemo, useState } from "react"
import { ScenarioChips } from "@/components/scenario-chip"
import { StartActions } from "@/components/start-actions"
import { StatusPill } from "@/components/status-pill"
import { Button } from "@/components/ui/button"
import {
  ColumnHeading,
  ConsoleFrame,
  DataTable,
} from "@/components/console-frame"
import { sessionsForEnvironment } from "@/lib/catalog"
import { matchesQuery } from "@/lib/format"
import { useCatalog, useSandboxActions, useSessions } from "@/lib/use-console"
import type { CatalogEnvironment, Session } from "@/lib/types"

export const Route = createFileRoute("/environments/")({
  component: EnvironmentsPage,
})

function EnvironmentsPage() {
  const { catalog, error } = useCatalog()
  const [sessions, setSessions] = useSessions()
  const { pending, errors, start, close } = useSandboxActions()
  const [query, setQuery] = useState("")
  const loaded = sessions ?? []

  const environments = useMemo(
    () =>
      (catalog?.environments ?? []).filter((environment) =>
        matchesQuery(query, [
          environment.name,
          environment.label,
          ...environment.services.flatMap((service) => [
            service.name,
            service.resource,
            service.displayName,
            service.scenario,
          ]),
        ])
      ),
    [catalog, query]
  )

  async function onStart(name: string, proxy: boolean) {
    await start("environment", name, proxy, "environment")
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
      crumbs={[{ label: "Environments" }]}
      heading="Environments"
      description="An environment packages resources and scenarios. Start brings every service in it up together."
      search={{
        value: query,
        onChange: setQuery,
        label: "Search environments",
      }}
    >
      {error ? <p className="text-sm text-destructive">{error}</p> : null}
      {catalog === null && !error ? (
        <p className="text-sm text-muted-foreground">Loading environments…</p>
      ) : null}
      {catalog !== null && environments.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          No environments match that search.
        </p>
      ) : null}
      {environments.length > 0 ? (
        <DataTable className="min-w-[880px]">
          <thead>
            <tr>
              <ColumnHeading>Environment</ColumnHeading>
              <ColumnHeading>Resources and scenarios</ColumnHeading>
              <ColumnHeading>Status</ColumnHeading>
              <ColumnHeading> </ColumnHeading>
            </tr>
          </thead>
          <tbody>
            {environments.map((environment) => (
              <EnvironmentRow
                key={environment.name}
                environment={environment}
                running={sessionsForEnvironment(loaded, environment.name)}
                sessionsKnown={sessions !== null}
                pending={pending}
                error={errors[`environment:${environment.name}`]}
                closeError={(id) => errors[`close:${id}`]}
                onStart={(proxy) => void onStart(environment.name, proxy)}
                onClose={(id) => void onClose(id)}
              />
            ))}
          </tbody>
        </DataTable>
      ) : null}
    </ConsoleFrame>
  )
}

function EnvironmentRow({
  environment,
  running,
  sessionsKnown,
  pending,
  error,
  closeError,
  onStart,
  onClose,
}: {
  environment: CatalogEnvironment
  running: Session[]
  sessionsKnown: boolean
  pending: string | null
  error?: string
  closeError: (id: string) => string | undefined
  onStart: (proxy: boolean) => void
  onClose: (id: string) => void
}) {
  const key = `environment:${environment.name}`
  const only = running.length === 1 ? running[0] : undefined
  return (
    <tr className="border-t align-top">
      <td className="px-3 py-3">
        <Link
          to="/environments/$name"
          params={{ name: environment.name }}
          className="underline decoration-muted-foreground/50 underline-offset-2"
        >
          {environment.label}
        </Link>
        <p className="font-mono text-xs text-muted-foreground">
          {environment.name}
        </p>
        <p className="text-xs text-muted-foreground">
          {environment.services.length}{" "}
          {environment.services.length === 1 ? "service" : "services"}
        </p>
        {error ? (
          <p className="mt-1 text-xs text-destructive">{error}</p>
        ) : null}
        {only && closeError(only.id) ? (
          <p className="mt-1 text-xs text-destructive">{closeError(only.id)}</p>
        ) : null}
      </td>
      <td className="px-3 py-3">
        <ScenarioChips services={environment.services} />
      </td>
      <td className="px-3 py-3">
        <EnvironmentStatus known={sessionsKnown} running={running} />
      </td>
      <td className="px-3 py-3">
        {!sessionsKnown ? (
          <p className="text-right text-sm text-muted-foreground">Checking…</p>
        ) : running.length === 0 ? (
          <StartActions
            pending={pending}
            directKey={`${key}:direct`}
            proxyKey={`${key}:proxy`}
            onStart={onStart}
            startLabel="Start all"
          />
        ) : (
          <div className="flex flex-wrap justify-end gap-2">
            <Button
              size="sm"
              variant="outline"
              render={
                <Link
                  to="/environments/$name"
                  params={{ name: environment.name }}
                />
              }
            >
              Manage
            </Button>
            {only && !only.anchored ? (
              <Button
                size="sm"
                variant="outline"
                disabled={pending !== null}
                onClick={() => onClose(only.id)}
              >
                {pending === `close:${only.id}` ? "Closing…" : "Close"}
              </Button>
            ) : null}
          </div>
        )}
      </td>
    </tr>
  )
}

function EnvironmentStatus({
  known,
  running,
}: {
  known: boolean
  running: Session[]
}) {
  if (!known) {
    return <span className="text-sm text-muted-foreground">Checking…</span>
  }
  if (running.length === 0) {
    return <StatusPill tone="neutral">Stopped</StatusPill>
  }
  if (running.length > 1) {
    return <StatusPill tone="ready">{running.length} running</StatusPill>
  }
  if (running[0]?.anchored) {
    return <StatusPill tone="held">Held by fab run</StatusPill>
  }
  return <StatusPill tone="ready">Ready</StatusPill>
}
