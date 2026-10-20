import { createFileRoute } from "@tanstack/react-router"
import { AgentCopy } from "@/components/agent-copy"
import { ErrorNote, LoadingLine } from "@/components/feedback"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/motion/card"
import { ScenarioChips } from "@/components/scenario-chip"
import { ServiceURLs } from "@/components/service-urls"
import { LinkButton, StatefulButton } from "@/components/ui/button"
import { ServiceIdentity } from "@/components/service-identity"
import { StartActions } from "@/components/start-actions"
import { StatusPill } from "@/components/status-pill"
import {
  ColumnHeading,
  ConsoleFrame,
  DataTable,
} from "@/components/console-frame"
import { environmentAgentBrief } from "@/lib/agent-brief"
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

  async function onStart() {
    const session = await start("environment", name, "stay")
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
        <LoadingLine>Loading environment…</LoadingLine>
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
          <LinkButton to="/environments">Back to environments</LinkButton>
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
        <div className="flex flex-wrap items-end gap-2">
          <AgentCopy brief={environmentAgentBrief(environment, sessions)} />
          {sessions === null ? null : only ? (
            <RunningActions
              session={only}
              pending={pending}
              onClose={() => void onClose(only.id)}
            />
          ) : running.length > 1 ? null : (
            <StartActions
              pending={pending}
              actionKey={key}
              onStart={() => void onStart()}
              label="Start environment"
            />
          )}
        </div>
      }
      sections={[
        { id: "running", label: "Running" },
        { id: "services", label: "Services" },
      ]}
    >
      {errors[key] ? <ErrorNote>{errors[key]}</ErrorNote> : null}
      <section id="running" className="flex scroll-mt-6 flex-col gap-3">
        <h2 className="text-lg font-medium">Running</h2>
        {sessions === null ? (
          <LoadingLine>Checking sandboxes…</LoadingLine>
        ) : running.length === 0 ? (
          <Card variant="muted" size="compact">
            <CardHeader>
              <CardTitle as="h3">Stopped</CardTitle>
              <CardDescription>
                Start environment brings up all {environment.services.length}{" "}
                {environment.services.length === 1 ? "service" : "services"} in
                one sandbox.
              </CardDescription>
            </CardHeader>
          </Card>
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
      <LinkButton
        variant="primary"
        to="/sandboxes/$id"
        params={{ id: session.id }}
      >
        Open sandbox
      </LinkButton>
      {session.anchored ? null : (
        <StatefulButton
          size="sm"
          variant="outline"
          state={pending === `close:${session.id}` ? "loading" : "idle"}
          loadingText="Closing…"
          disabled={pending !== null && pending !== `close:${session.id}`}
          onClick={onClose}
        >
          Close
        </StatefulButton>
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
    <Card size="compact">
      <CardContent className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div className="flex flex-col gap-2">
          {session.anchored ? (
            <StatusPill tone="held">Held by fab run</StatusPill>
          ) : (
            <StatusPill tone="ready">Ready</StatusPill>
          )}
          <p className="font-mono text-xs text-muted-foreground">
            {session.id}
          </p>
          <ServiceURLs services={session.services} />
          <p className="text-sm text-muted-foreground">
            Started {formatTime(session.startedAt)}
          </p>
          <ScenarioChips services={session.services} />
          {session.anchored ? (
            <p className="max-w-xl text-sm text-muted-foreground">
              Held by fab run. It stays up until that command exits.
            </p>
          ) : null}
          {error ? <ErrorNote compact>{error}</ErrorNote> : null}
        </div>
        <RunningActions session={session} pending={pending} onClose={onClose} />
      </CardContent>
    </Card>
  )
}

function joinNames(names: string[]): string {
  if (names.length === 0) return "its services"
  if (names.length === 1) return names[0]
  if (names.length === 2) return `${names[0]} and ${names[1]}`
  return `${names.slice(0, -1).join(", ")}, and ${names[names.length - 1]}`
}
