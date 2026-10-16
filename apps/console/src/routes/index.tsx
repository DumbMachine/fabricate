import { Link, createFileRoute, useNavigate } from "@tanstack/react-router"
import { useEffect, useMemo, useState, type ReactNode } from "react"
import { Button } from "@/components/ui/button"
import { ConsoleHeader } from "@/components/console-header"
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

  return (
    <div className="min-h-svh">
      <ConsoleHeader />
      <main className="mx-auto flex max-w-6xl flex-col gap-8 px-6 py-8">
        <section className="flex flex-col gap-2">
          <h1 className="text-2xl font-medium tracking-tight">
            Start a sandbox
          </h1>
          <p className="max-w-2xl text-sm leading-6 text-muted-foreground">
            Browse an environment or a scenario, start it, then connect your app
            or hand the running sandbox to a coding agent. Sandboxes stay on
            this machine until you close them.
          </p>
        </section>

        <section className="flex flex-col gap-3">
          <h2 className="text-sm font-medium">Running</h2>
          {sessions.length === 0 ? (
            <p className="rounded-xl border border-dashed px-4 py-6 text-sm text-muted-foreground">
              Nothing is running. Start an environment or a scenario below.
            </p>
          ) : (
            <ul className="flex flex-col gap-3">
              {sessions.map((session) => (
                <li
                  key={session.id}
                  className="flex flex-col gap-3 rounded-xl border bg-card p-4 sm:flex-row sm:items-center sm:justify-between"
                >
                  <div className="min-w-0">
                    <Link
                      to="/sandboxes/$id"
                      params={{ id: session.id }}
                      className="font-medium"
                    >
                      {session.label}
                    </Link>
                    <p className="truncate text-sm text-muted-foreground">
                      {session.name} · {kindLabel(session.kind)} ·{" "}
                      {session.proxy ? "proxy" : "direct"} ·{" "}
                      {formatTime(session.startedAt)}
                    </p>
                    {errors[`close:${session.id}`] ? (
                      <p className="mt-1 text-sm text-destructive">
                        {errors[`close:${session.id}`]}
                      </p>
                    ) : null}
                  </div>
                  <div className="flex gap-2">
                    {session.anchored ? (
                      <p className="self-center text-sm text-muted-foreground">
                        Held by fab run
                      </p>
                    ) : (
                      <Button
                        variant="outline"
                        size="sm"
                        disabled={pending === `close:${session.id}`}
                        onClick={() => void close(session.id)}
                      >
                        Close
                      </Button>
                    )}
                    <Button
                      render={
                        <Link to="/sandboxes/$id" params={{ id: session.id }} />
                      }
                      size="sm"
                    >
                      Open
                    </Button>
                  </div>
                </li>
              ))}
            </ul>
          )}
        </section>

        <section className="flex flex-col gap-4">
          <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
            <div className="inline-flex w-fit rounded-lg bg-muted p-1">
              <TabButton
                selected={tab === "environments"}
                onClick={() => setTab("environments")}
              >
                Environments ({catalog?.environments.length ?? 0})
              </TabButton>
              <TabButton
                selected={tab === "scenarios"}
                onClick={() => setTab("scenarios")}
              >
                Scenarios ({catalog?.scenarios.length ?? 0})
              </TabButton>
            </div>
            <input
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              placeholder="Search name, resource, or scenario"
              aria-label="Search environments and scenarios"
              className="h-8 w-full rounded-lg border bg-background px-2.5 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 sm:max-w-xs"
            />
          </div>
          {catalogError ? (
            <p className="text-sm text-destructive">{catalogError}</p>
          ) : null}
          {catalog === null && !catalogError ? (
            <p className="text-sm text-muted-foreground">Loading catalog…</p>
          ) : null}
          {tab === "environments" ? (
            <div className="grid gap-4 md:grid-cols-2">
              {environments.map((environment) => (
                <EnvironmentCard
                  key={environment.name}
                  environment={environment}
                  pending={pending}
                  error={errors[`environment:${environment.name}`] ?? ""}
                  onStart={(proxy) =>
                    void start("environment", environment.name, proxy)
                  }
                />
              ))}
            </div>
          ) : (
            <div className="grid gap-4 md:grid-cols-2">
              {scenarios.map((scenario) => (
                <ScenarioCard
                  key={scenario.id}
                  scenario={scenario}
                  pending={pending}
                  error={errors[`scenario:${scenario.id}`] ?? ""}
                  onStart={(proxy) =>
                    void start("scenario", scenario.id, proxy)
                  }
                />
              ))}
            </div>
          )}
          {catalog && tab === "environments" && environments.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              No environments match that search.
            </p>
          ) : null}
          {catalog && tab === "scenarios" && scenarios.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              No scenarios match that search.
            </p>
          ) : null}
        </section>
      </main>
    </div>
  )
}

function EnvironmentCard({
  environment,
  pending,
  error,
  onStart,
}: {
  environment: CatalogEnvironment
  pending: string | null
  error: string
  onStart: (proxy: boolean) => void
}) {
  const key = `environment:${environment.name}`
  return (
    <article className="flex flex-col gap-4 rounded-xl border bg-card p-4">
      <div className="flex flex-col gap-1">
        <h3 className="font-medium">{environment.label}</h3>
        <p className="font-mono text-xs text-muted-foreground">
          {environment.name}
        </p>
      </div>
      <ul className="flex flex-col gap-2 text-sm">
        {environment.services.map((service) => (
          <li key={service.name}>
            <span className="font-medium">{service.displayName}</span>
            <span className="text-muted-foreground">
              {" "}
              · {service.name} · {service.scenario}
            </span>
          </li>
        ))}
      </ul>
      <StartActions
        pending={pending}
        directKey={`${key}:direct`}
        proxyKey={`${key}:proxy`}
        error={error}
        onStart={onStart}
      />
    </article>
  )
}

function ScenarioCard({
  scenario,
  pending,
  error,
  onStart,
}: {
  scenario: CatalogScenario
  pending: string | null
  error: string
  onStart: (proxy: boolean) => void
}) {
  const key = `scenario:${scenario.id}`
  return (
    <article className="flex flex-col gap-4 rounded-xl border bg-card p-4">
      <div className="flex flex-col gap-1">
        <h3 className="font-medium">{scenario.displayName}</h3>
        <p className="font-mono text-xs text-muted-foreground">{scenario.id}</p>
      </div>
      <p className="text-sm text-muted-foreground">
        {scenario.providerHosts.join(", ") || "No production hostname"}
      </p>
      <StartActions
        pending={pending}
        directKey={`${key}:direct`}
        proxyKey={`${key}:proxy`}
        error={error}
        onStart={onStart}
      />
    </article>
  )
}

function StartActions({
  pending,
  directKey,
  proxyKey,
  error,
  onStart,
}: {
  pending: string | null
  directKey: string
  proxyKey: string
  error: string
  onStart: (proxy: boolean) => void
}) {
  const busy = pending === directKey || pending === proxyKey
  return (
    <div className="mt-auto flex flex-col gap-2">
      {error ? <p className="text-sm text-destructive">{error}</p> : null}
      <div className="flex flex-wrap gap-2">
        <Button type="button" disabled={busy} onClick={() => onStart(false)}>
          {pending === directKey ? "Starting…" : "Start"}
        </Button>
        <Button
          type="button"
          variant="outline"
          disabled={busy}
          onClick={() => onStart(true)}
        >
          {pending === proxyKey ? "Starting…" : "Start with proxy"}
        </Button>
      </div>
    </div>
  )
}

function TabButton({
  selected,
  onClick,
  children,
}: {
  selected: boolean
  onClick: () => void
  children: ReactNode
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={
        selected
          ? "rounded-md bg-background px-3 py-1 text-sm font-medium shadow-sm"
          : "rounded-md px-3 py-1 text-sm text-muted-foreground"
      }
    >
      {children}
    </button>
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
