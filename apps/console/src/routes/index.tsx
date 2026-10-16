import { Link, createFileRoute, useNavigate } from "@tanstack/react-router"
import { useEffect, useMemo, useState } from "react"
import { Button } from "@/components/ui/button"
import {
  ColumnHeading,
  ConsoleFrame,
  DataTable,
  FilterTabs,
} from "@/components/console-frame"
import { StatusPill } from "@/components/status-pill"
import { closeSandbox, getCatalog, listSessions, startSandbox } from "@/lib/api"
import { formatTime, kindLabel } from "@/lib/format"
import type {
  Catalog,
  CatalogEnvironment,
  CatalogScenario,
  SandboxKind,
  Session,
} from "@/lib/types"

export const Route = createFileRoute("/")({ component: CatalogPage })

function CatalogPage() {
  const navigate = useNavigate()
  const [catalog, setCatalog] = useState<Catalog | null>(null)
  const [sessions, setSessions] = useState<Session[]>([])
  const [catalogError, setCatalogError] = useState<string | null>(null)
  const [query, setQuery] = useState("")
  const [tab, setTab] = useState<"environments" | "scenarios">("environments")
  const [runningFilter, setRunningFilter] = useState<
    "all" | "direct" | "proxy"
  >("all")
  const [pending, setPending] = useState<string | null>(null)
  const [errors, setErrors] = useState<Record<string, string>>({})

  useEffect(() => {
    let cancelled = false
    getCatalog()
      .then((next) => {
        if (!cancelled) setCatalog(next)
      })
      .catch((error: unknown) => {
        if (!cancelled) setCatalogError(message(error))
      })
    return () => {
      cancelled = true
    }
  }, [])

  useEffect(() => {
    let cancelled = false
    const tick = () => {
      listSessions()
        .then((next) => {
          if (!cancelled) setSessions(next)
        })
        .catch(() => {
          if (!cancelled) setSessions([])
        })
    }
    tick()
    const timer = window.setInterval(tick, 2000)
    return () => {
      cancelled = true
      window.clearInterval(timer)
    }
  }, [])

  const visibleSessions = useMemo(
    () =>
      sessions.filter((session) => {
        if (runningFilter === "proxy" && !session.proxy) return false
        if (runningFilter === "direct" && session.proxy) return false
        return matches(query, [
          session.name,
          session.label,
          session.kind,
          session.id,
        ])
      }),
    [sessions, runningFilter, query]
  )
  const environments = useMemo(
    () =>
      (catalog?.environments ?? []).filter((environment) =>
        matches(query, [
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
  const scenarios = useMemo(
    () =>
      (catalog?.scenarios ?? []).filter((scenario) =>
        matches(query, [
          scenario.id,
          scenario.resource,
          scenario.displayName,
          ...scenario.providerHosts,
        ])
      ),
    [catalog, query]
  )

  async function start(kind: SandboxKind, id: string, proxy: boolean) {
    const key = `${kind}:${id}`
    setPending(`${key}:${proxy ? "proxy" : "direct"}`)
    setErrors((current) => ({ ...current, [key]: "" }))
    try {
      const session = await startSandbox(kind, id, proxy)
      await navigate({ to: "/sandboxes/$id", params: { id: session.id } })
    } catch (error: unknown) {
      setErrors((current) => ({ ...current, [key]: message(error) }))
    } finally {
      setPending(null)
    }
  }

  async function close(id: string) {
    setPending(`close:${id}`)
    try {
      await closeSandbox(id)
      setSessions((current) => current.filter((session) => session.id !== id))
    } catch (error: unknown) {
      setErrors((current) => ({ ...current, [`close:${id}`]: message(error) }))
    } finally {
      setPending(null)
    }
  }

  const directCount = sessions.filter((session) => !session.proxy).length
  const proxyCount = sessions.filter((session) => session.proxy).length

  return (
    <ConsoleFrame
      crumbs={[{ label: "Sandboxes" }]}
      heading="Overview"
      description="Start an environment or a scenario, then connect your app or hand the sandbox to a coding agent."
      search={{ value: query, onChange: setQuery }}
    >
      <section className="flex flex-col gap-3">
        <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <h2 className="text-lg font-medium">Running</h2>
          <FilterTabs
            value={runningFilter}
            onChange={(id) =>
              setRunningFilter(id as "all" | "direct" | "proxy")
            }
            options={[
              { id: "all", label: `All (${sessions.length})` },
              { id: "direct", label: `Direct (${directCount})` },
              { id: "proxy", label: `Proxy (${proxyCount})` },
            ]}
          />
        </div>
        {visibleSessions.length === 0 ? (
          <div className="border bg-card px-4 py-8">
            <h3 className="text-lg font-medium">Nothing is running</h3>
            <p className="mt-1 max-w-xl text-sm text-muted-foreground">
              Start an environment or a scenario from the catalog. A sandbox
              stays on this machine until you close it.
            </p>
          </div>
        ) : (
          <DataTable>
            <thead>
              <tr>
                <ColumnHeading>Name</ColumnHeading>
                <ColumnHeading>Status</ColumnHeading>
                <ColumnHeading>Kind</ColumnHeading>
                <ColumnHeading>Mode</ColumnHeading>
                <ColumnHeading>Started</ColumnHeading>
                <ColumnHeading> </ColumnHeading>
              </tr>
            </thead>
            <tbody>
              {visibleSessions.map((session) => (
                <tr key={session.id} className="border-t">
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
                    {errors[`close:${session.id}`] ? (
                      <p className="mt-1 text-xs text-destructive">
                        {errors[`close:${session.id}`]}
                      </p>
                    ) : null}
                  </td>
                  <td className="px-3 py-3">
                    {session.anchored ? (
                      <StatusPill tone="held">Held by fab run</StatusPill>
                    ) : (
                      <StatusPill tone="ready">Ready</StatusPill>
                    )}
                  </td>
                  <td className="px-3 py-3 text-muted-foreground">
                    {kindLabel(session.kind)}
                  </td>
                  <td className="px-3 py-3">
                    <span className="rounded bg-muted px-1.5 py-0.5 text-xs">
                      {session.proxy ? "Proxy" : "Direct"}
                    </span>
                  </td>
                  <td className="px-3 py-3 text-muted-foreground">
                    {formatTime(session.startedAt)}
                  </td>
                  <td className="px-3 py-3 text-right">
                    {session.anchored ? null : (
                      <Button
                        variant="outline"
                        size="sm"
                        disabled={pending === `close:${session.id}`}
                        onClick={() => void close(session.id)}
                      >
                        Close
                      </Button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </DataTable>
        )}
      </section>

      <section className="flex flex-col gap-3">
        <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <h2 className="text-lg font-medium">Catalog</h2>
          <FilterTabs
            value={tab}
            onChange={(id) => setTab(id as "environments" | "scenarios")}
            options={[
              {
                id: "environments",
                label: `Environments (${catalog?.environments.length ?? 0})`,
              },
              {
                id: "scenarios",
                label: `Scenarios (${catalog?.scenarios.length ?? 0})`,
              },
            ]}
          />
        </div>
        {catalogError ? (
          <p className="text-sm text-destructive">{catalogError}</p>
        ) : null}
        {catalog === null && !catalogError ? (
          <p className="text-sm text-muted-foreground">Loading catalog…</p>
        ) : null}
        {tab === "environments" ? (
          <EnvironmentTable
            environments={environments}
            pending={pending}
            errors={errors}
            onStart={(name, proxy) => void start("environment", name, proxy)}
          />
        ) : (
          <ScenarioTable
            scenarios={scenarios}
            pending={pending}
            errors={errors}
            onStart={(id, proxy) => void start("scenario", id, proxy)}
          />
        )}
      </section>
    </ConsoleFrame>
  )
}

function EnvironmentTable({
  environments,
  pending,
  errors,
  onStart,
}: {
  environments: CatalogEnvironment[]
  pending: string | null
  errors: Record<string, string>
  onStart: (name: string, proxy: boolean) => void
}) {
  if (environments.length === 0) {
    return (
      <p className="text-sm text-muted-foreground">
        No environments match that search.
      </p>
    )
  }
  return (
    <DataTable>
      <thead>
        <tr>
          <ColumnHeading>Environment</ColumnHeading>
          <ColumnHeading>Services</ColumnHeading>
          <ColumnHeading> </ColumnHeading>
        </tr>
      </thead>
      <tbody>
        {environments.map((environment) => {
          const key = `environment:${environment.name}`
          return (
            <tr key={environment.name} className="border-t">
              <td className="px-3 py-3">
                <p className="font-medium">{environment.label}</p>
                <p className="font-mono text-xs text-muted-foreground">
                  {environment.name}
                </p>
                {errors[key] ? (
                  <p className="mt-1 text-xs text-destructive">{errors[key]}</p>
                ) : null}
              </td>
              <td className="px-3 py-3 text-muted-foreground">
                {environment.services
                  .map((service) => service.displayName)
                  .join(", ")}
              </td>
              <td className="px-3 py-3">
                <StartActions
                  pending={pending}
                  directKey={`${key}:direct`}
                  proxyKey={`${key}:proxy`}
                  onStart={(proxy) => onStart(environment.name, proxy)}
                />
              </td>
            </tr>
          )
        })}
      </tbody>
    </DataTable>
  )
}

function ScenarioTable({
  scenarios,
  pending,
  errors,
  onStart,
}: {
  scenarios: CatalogScenario[]
  pending: string | null
  errors: Record<string, string>
  onStart: (id: string, proxy: boolean) => void
}) {
  if (scenarios.length === 0) {
    return (
      <p className="text-sm text-muted-foreground">
        No scenarios match that search.
      </p>
    )
  }
  return (
    <DataTable>
      <thead>
        <tr>
          <ColumnHeading>Scenario</ColumnHeading>
          <ColumnHeading>Resource</ColumnHeading>
          <ColumnHeading>Production hostnames</ColumnHeading>
          <ColumnHeading> </ColumnHeading>
        </tr>
      </thead>
      <tbody>
        {scenarios.map((scenario) => {
          const key = `scenario:${scenario.id}`
          return (
            <tr key={scenario.id} className="border-t">
              <td className="px-3 py-3">
                <p className="font-medium">{scenario.displayName}</p>
                <p className="font-mono text-xs text-muted-foreground">
                  {scenario.id}
                </p>
                {errors[key] ? (
                  <p className="mt-1 text-xs text-destructive">{errors[key]}</p>
                ) : null}
              </td>
              <td className="px-3 py-3 text-muted-foreground">
                {scenario.resource}
              </td>
              <td className="px-3 py-3 text-muted-foreground">
                {scenario.providerHosts.join(", ") || "None"}
              </td>
              <td className="px-3 py-3">
                <StartActions
                  pending={pending}
                  directKey={`${key}:direct`}
                  proxyKey={`${key}:proxy`}
                  onStart={(proxy) => onStart(scenario.id, proxy)}
                />
              </td>
            </tr>
          )
        })}
      </tbody>
    </DataTable>
  )
}

function StartActions({
  pending,
  directKey,
  proxyKey,
  onStart,
}: {
  pending: string | null
  directKey: string
  proxyKey: string
  onStart: (proxy: boolean) => void
}) {
  const busy = pending === directKey || pending === proxyKey
  return (
    <div className="flex justify-end gap-2">
      <Button
        type="button"
        size="sm"
        disabled={busy}
        onClick={() => onStart(false)}
      >
        {pending === directKey ? "Starting…" : "Start"}
      </Button>
      <Button
        type="button"
        variant="outline"
        size="sm"
        disabled={busy}
        onClick={() => onStart(true)}
      >
        {pending === proxyKey ? "Starting…" : "Proxy"}
      </Button>
    </div>
  )
}

function matches(query: string, parts: string[]): boolean {
  const needle = query.trim().toLowerCase()
  if (!needle) return true
  return parts.join("\n").toLowerCase().includes(needle)
}

function message(error: unknown): string {
  return error instanceof Error ? error.message : "Request failed"
}
