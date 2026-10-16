import { Link, createFileRoute } from "@tanstack/react-router"
import { ScenarioChips } from "@/components/scenario-chip"
import { Button } from "@/components/ui/button"
import { ServiceIdentity } from "@/components/service-identity"
import { StartActions } from "@/components/start-actions"
import { StatusPill } from "@/components/status-pill"
import {
  ColumnHeading,
  ConsoleFrame,
  DataTable,
} from "@/components/console-frame"
import { sessionsForEnvironment } from "@/lib/catalog"
import { formatTime } from "@/lib/format"
import { useCatalog, useSandboxActions, useSessions } from "@/lib/use-console"
import type { Session } from "@/lib/types"

export const Route = createFileRoute("/environments/$name")({
  component: EnvironmentPage,
})

function EnvironmentPage() {
  const { name } = Route.useParams()
  const { catalog, error: catalogError } = useCatalog()
  const [sessions, setSessions] = useSessions()
  const { pending, errors, start, close } = useSandboxActions()
  const environment =
    catalog?.environments.find((item) => item.name === name) ?? null
  const running = sessionsForEnvironment(sessions ?? [], name)
  const key = `environment:${name}`

  async function onStart(proxy: boolean) {
    const session = await start("environment", name, proxy, "stay")
    if (session) {
      setSessions((current) => [
        session,
        ...(current ?? []).filter((item) => item.id !== session.id),
      ])
    }
  }

  async function onClose(id: string) {
    const ok = await close(id)
    if (ok) {
      setSessions((current) => (current ?? []).filter((item) => item.id !== id))
    }
  }

  if (catalog === null && !catalogError) {
    return (
      <ConsoleFrame
        crumbs={[
          { label: "Environments", to: "/environments" },
          { label: name },
        ]}
      >
        <p className="text-sm text-muted-foreground">Loading environment…</p>
      </ConsoleFrame>
    )
  }
  if (!environment) {
    return (
      <ConsoleFrame
        crumbs={[
          { label: "Environments", to: "/environments" },
          { label: "Unknown" },
        ]}
        eyebrow="Environment"
        heading="Environment not found"
        description={catalogError ?? `${name} is not an official environment.`}
        actions={
          <Button render={<Link to="/environments" />} variant="outline">
            Back to environments
          </Button>
        }
      >
        {null}
      </ConsoleFrame>
    )
  }

  const only = running.length === 1 ? running[0] : undefined
  const serviceNames = environment.services.map(
    (service) => service.displayName
  )

  return (
    <ConsoleFrame
      crumbs={[
        { label: "Environments", to: "/environments" },
        { label: environment.label },
      ]}
      eyebrow="Environment"
      heading={environment.label}
      description={`${environment.name} starts ${joinNames(serviceNames)} together. Each service loads the scenario listed below.`}
      actions={
        sessions === null ? null : only ? (
          <RunningActions
            session={only}
            pending={pending}
            onClose={() => void onClose(only.id)}
          />
        ) : running.length > 1 ? null : (
          <StartActions
            pending={pending}
            directKey={`${key}:direct`}
            proxyKey={`${key}:proxy`}
            onStart={(proxy) => void onStart(proxy)}
            startLabel="Start environment"
          />
        )
      }
      sections={[
        { id: "running", label: "Running" },
        { id: "services", label: "Services" },
      ]}
    >
      {errors[key] ? (
        <p className="text-sm text-destructive">{errors[key]}</p>
      ) : null}
      <section id="running" className="flex scroll-mt-6 flex-col gap-3">
        <h2 className="text-lg font-medium">Running</h2>
        {sessions === null ? (
          <p className="text-sm text-muted-foreground">Checking sandboxes…</p>
        ) : running.length === 0 ? (
          <div className="border bg-card px-4 py-6">
            <h3 className="font-medium">Stopped</h3>
            <p className="mt-1 max-w-xl text-sm text-muted-foreground">
              Start environment brings up all {environment.services.length}{" "}
              {environment.services.length === 1 ? "service" : "services"} in
              one sandbox. Proxy does the same through the transparent proxy.
            </p>
          </div>
        ) : (
          <div className="flex flex-col gap-3">
            {running.map((session) => (
              <RunningSession
                key={session.id}
                session={session}
                pending={pending}
                error={errors[`close:${session.id}`]}
                onClose={() => void onClose(session.id)}
              />
            ))}
          </div>
        )}
      </section>

      <section id="services" className="flex scroll-mt-6 flex-col gap-3">
        <h2 className="text-lg font-medium">Services</h2>
        <DataTable>
          <thead>
            <tr>
              <ColumnHeading>Service</ColumnHeading>
              <ColumnHeading>Production hostnames</ColumnHeading>
            </tr>
          </thead>
          <tbody>
            {environment.services.map((service) => (
              <tr key={service.name} className="border-t align-top">
                <td className="px-3 py-3">
                  <ServiceIdentity
                    resource={service.resource}
                    displayName={service.displayName}
                    scenario={service.scenario}
                    detail={service.name}
                  />
                </td>
                <td className="px-3 py-3 text-muted-foreground">
                  {service.providerHosts.join(", ") || "None"}
                </td>
              </tr>
            ))}
          </tbody>
        </DataTable>
      </section>
    </ConsoleFrame>
  )
}

function RunningActions({
  session,
  pending,
  onClose,
}: {
  session: Session
  pending: string | null
  onClose: () => void
}) {
  return (
    <div className="flex flex-wrap gap-2">
      <Button
        size="sm"
        render={<Link to="/sandboxes/$id" params={{ id: session.id }} />}
      >
        Open sandbox
      </Button>
      {session.anchored ? null : (
        <Button
          size="sm"
          variant="outline"
          disabled={pending !== null}
          onClick={onClose}
        >
          {pending === `close:${session.id}` ? "Closing…" : "Close"}
        </Button>
      )}
    </div>
  )
}

function RunningSession({
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
    <div className="flex flex-col gap-3 border bg-card px-4 py-4 sm:flex-row sm:items-start sm:justify-between">
      <div className="flex flex-col gap-2">
        {session.anchored ? (
          <StatusPill tone="held">Held by fab run</StatusPill>
        ) : (
          <StatusPill tone="ready">Ready</StatusPill>
        )}
        <p className="font-mono text-xs text-muted-foreground">{session.id}</p>
        <p className="text-sm text-muted-foreground">
          {session.proxy ? "Proxy" : "Direct"} · started{" "}
          {formatTime(session.startedAt)}
        </p>
        <ScenarioChips services={session.services} />
        {session.anchored ? (
          <p className="max-w-xl text-sm text-muted-foreground">
            Held by fab run. It stays up until that command exits.
          </p>
        ) : null}
        {error ? <p className="text-xs text-destructive">{error}</p> : null}
      </div>
      <RunningActions session={session} pending={pending} onClose={onClose} />
    </div>
  )
}

function joinNames(names: string[]): string {
  if (names.length === 0) return "its services"
  if (names.length === 1) return names[0]
  if (names.length === 2) return `${names[0]} and ${names[1]}`
  return `${names.slice(0, -1).join(", ")}, and ${names[names.length - 1]}`
}
