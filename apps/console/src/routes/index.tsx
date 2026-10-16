import { Link, createFileRoute } from "@tanstack/react-router"
import { useMemo, useState } from "react"
import { ErrorNote, LoadingLine } from "@/components/feedback"
import {
  Card,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/motion/card"
import { ScenarioChips } from "@/components/scenario-chip"
import { ResourceIcon } from "@/components/resource-icon"
import { StartActions } from "@/components/start-actions"
import { StatusPill } from "@/components/status-pill"
import { StatefulButton } from "@/components/ui/button"
import {
  ColumnHeading,
  ConsoleFrame,
  DataTable,
} from "@/components/console-frame"
import { groupScenarios } from "@/lib/catalog"
import { formatTime, kindLabel, matchesQuery } from "@/lib/format"
import { useCatalog, useSandboxActions, useSessions } from "@/lib/use-console"
import type { CatalogScenario, Session } from "@/lib/types"

export const Route = createFileRoute("/")({ component: SandboxesPage })

function SandboxesPage() {
  const { catalog, error: catalogError } = useCatalog()
  const [sessions, setSessions] = useSessions()
  const { pending, errors, start, close } = useSandboxActions()
  const [query, setQuery] = useState("")
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
        ]),
      ])
    )
  }, [sessions, query])
  const groups = useMemo(
    () =>
      groupScenarios(
        (catalog?.scenarios ?? []).filter((scenario) =>
          matchesQuery(query, [
            scenario.id,
            scenario.resource,
            scenario.displayName,
            scenario.resourceVersion,
            ...scenario.providerHosts,
          ])
        )
      ),
    [catalog, query]
  )

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
      description="A running sandbox shows the scenario that is active. Start one scenario here, or start every service in an environment together."
      search={{
        value: query,
        onChange: setQuery,
        label: "Search sandboxes and scenarios",
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
                Start a scenario below, or open{" "}
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
          <DataTable className="min-w-[760px]">
            <thead>
              <tr>
                <ColumnHeading>Sandbox</ColumnHeading>
                <ColumnHeading>Active scenarios</ColumnHeading>
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
          Scenarios ({catalog?.scenarios.length ?? 0})
        </h2>
        <p className="max-w-2xl text-sm text-muted-foreground">
          Scenarios are grouped by resource. Open one to inspect its seeded
          records, or start it as its own sandbox.
        </p>
        {catalogError ? <ErrorNote>{catalogError}</ErrorNote> : null}
        {catalog === null && !catalogError ? (
          <LoadingLine>Loading scenarios…</LoadingLine>
        ) : null}
        {catalog !== null && groups.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            No scenarios match that search.
          </p>
        ) : null}
        {groups.map((group) => (
          <section key={group.resource} className="flex flex-col gap-2">
            <div className="flex items-center gap-2">
              <ResourceIcon id={group.resource} />
              <h3 className="font-medium">{group.displayName}</h3>
              <span className="font-mono text-xs text-muted-foreground">
                {group.resource}
              </span>
            </div>
            <DataTable>
              <thead>
                <tr>
                  <ColumnHeading>Scenario</ColumnHeading>
                  <ColumnHeading>Version</ColumnHeading>
                  <ColumnHeading>Production hostnames</ColumnHeading>
                  <ColumnHeading> </ColumnHeading>
                </tr>
              </thead>
              <tbody>
                {group.scenarios.map((scenario) => (
                  <ScenarioRow
                    key={scenario.id}
                    scenario={scenario}
                    pending={pending}
                    error={errors[`scenario:${scenario.id}`]}
                    onStart={() => void onStart(scenario.id)}
                  />
                ))}
              </tbody>
            </DataTable>
          </section>
        ))}
      </section>
    </ConsoleFrame>
  )
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
        <Link
          to="/sandboxes/$id"
          params={{ id: session.id }}
          className="underline decoration-muted-foreground/50 underline-offset-2"
        >
          {session.label}
        </Link>
        <p className="font-mono text-xs text-muted-foreground">
          {session.name}
        </p>
        <p className="text-xs text-muted-foreground">
          {kindLabel(session.kind)}
        </p>
        {error ? (
          <div className="mt-1">
            <ErrorNote compact>{error}</ErrorNote>
          </div>
        ) : null}
      </td>
      <td className="px-3 py-3">
        <ScenarioChips services={session.services} />
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

function ScenarioRow({
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
  const key = `scenario:${scenario.id}`
  return (
    <tr className="border-t align-top">
      <td className="px-3 py-3">
        <ScenarioChips
          services={[{ resource: scenario.resource, scenario: scenario.id }]}
        />
        {error ? (
          <div className="mt-1">
            <ErrorNote compact>{error}</ErrorNote>
          </div>
        ) : null}
      </td>
      <td className="px-3 py-3 font-mono text-xs text-muted-foreground">
        {scenario.resourceVersion}
      </td>
      <td className="px-3 py-3 text-muted-foreground">
        {scenario.providerHosts.join(", ") || "None"}
      </td>
      <td className="px-3 py-3">
        <StartActions pending={pending} actionKey={key} onStart={onStart} />
      </td>
    </tr>
  )
}
