import { createFileRoute } from "@tanstack/react-router"
import { useState } from "react"
import {
  ConsoleFrame,
  ColumnHeading,
  DataTable,
} from "@/components/console-frame"
import { PageTabs, TabPanel } from "@/components/page-tabs"
import { AgentCopy } from "@/components/agent-copy"
import { CloseSandboxButton } from "@/components/close-sandbox"
import { ErrorNote, LoadingLine } from "@/components/feedback"
import { LinkButton } from "@/components/ui/button"
import { StartActions } from "@/components/start-actions"
import { ServiceIdentity } from "@/components/service-identity"
import { StatusPill } from "@/components/status-pill"
import { environmentAgentBrief } from "@/lib/agent-brief"
import { sessionsForEnvironment } from "@/lib/catalog"
import { formatTime } from "@/lib/format"
import { useCatalog, useSessions, useSandboxActions } from "@/lib/use-console"
export const Route = createFileRoute("/environments/$name")({
  component: EnvironmentPage,
})
function EnvironmentPage() {
  const { name } = Route.useParams()
  const { catalog, error } = useCatalog()
  const [sessions, setSessions, sessionsError] = useSessions()
  const { pending, errors, start, close } = useSandboxActions()
  const [tab, setTab] = useState("services")
  const environment = catalog?.environments.find((item) => item.name === name)
  const running = sessionsForEnvironment(sessions || [], name)
  async function onStart() {
    const session = await start("environment", name, "stay")
    if (session)
      setSessions((current) => [
        session,
        ...(current || []).filter((s) => s.id !== session.id),
      ])
  }
  async function onClose(id: string) {
    if (await close(id))
      setSessions((current) => (current || []).filter((s) => s.id !== id))
  }
  if (!environment)
    return (
      <ConsoleFrame
        crumbs={[
          { label: "Environments", to: "/environments" },
          { label: name },
        ]}
      >
        {error ? (
          <ErrorNote>{error}</ErrorNote>
        ) : catalog ? (
          <p className="text-sm text-muted-foreground">
            This environment is not in the catalog.
          </p>
        ) : (
          <LoadingLine>Loading environment…</LoadingLine>
        )}
      </ConsoleFrame>
    )
  return (
    <ConsoleFrame
      crumbs={[
        { label: "Environments", to: "/environments" },
        { label: environment.label },
      ]}
      actions={
        <StartActions
          pending={pending}
          actionKey={`environment:${name}`}
          onStart={() => void onStart()}
          label={running.length ? "New sandbox" : "Start environment"}
        />
      }
      tabs={
        <PageTabs
          id="environment"
          label="Environment views"
          value={tab}
          onChange={setTab}
          items={[
            {
              id: "services",
              label: "Services",
              count: environment.services.length,
            },
            {
              id: "sandboxes",
              label: "Sandboxes",
              count: sessions ? running.length : undefined,
            },
          ]}
        />
      }
    >
      {errors[`environment:${name}`] ? (
        <ErrorNote>{errors[`environment:${name}`]}</ErrorNote>
      ) : null}
      {sessionsError ? <ErrorNote>{sessionsError}</ErrorNote> : null}
      <TabPanel id="environment" value={tab}>
        {tab === "services" ? (
          <>
            <p className="text-[13px] leading-6 text-muted-foreground">
              Start these services together in an isolated sandbox. Each loads
              the starting data listed below.
            </p>
            <DataTable>
              <thead>
                <tr>
                  <ColumnHeading>Service and scenario</ColumnHeading>
                  <ColumnHeading>Provider hostnames</ColumnHeading>
                </tr>
              </thead>
              <tbody>
                {environment.services.map((service) => (
                  <tr key={service.name} className="border-b align-top">
                    <td className="px-3 py-4">
                      <ServiceIdentity
                        resource={service.resource}
                        displayName={service.displayName}
                        scenario={service.scenario}
                        detail={service.name}
                      />
                    </td>
                    <td className="px-3 py-4 text-xs text-muted-foreground">
                      {service.providerHosts.join(", ") || "—"}
                    </td>
                  </tr>
                ))}
              </tbody>
            </DataTable>
            <div className="pt-3">
              <AgentCopy brief={environmentAgentBrief(environment, sessions)} />
            </div>
          </>
        ) : sessions === null ? (
          <LoadingLine>Loading sandboxes…</LoadingLine>
        ) : running.length === 0 ? (
          <div className="py-16 text-center">
            <h2 className="text-sm font-medium">No sandboxes running</h2>
            <p className="mt-2 text-[13px] text-muted-foreground">
              Start this environment to get service endpoints and credentials.
            </p>
          </div>
        ) : (
          <DataTable>
            <thead>
              <tr>
                <ColumnHeading>Sandbox</ColumnHeading>
                <ColumnHeading>Status</ColumnHeading>
                <ColumnHeading>Started</ColumnHeading>
                <ColumnHeading>Actions</ColumnHeading>
              </tr>
            </thead>
            <tbody>
              {running.map((session) => (
                <tr key={session.id} className="border-b">
                  <td className="px-3 py-4 font-mono text-xs">
                    {session.id}
                    {errors[`close:${session.id}`] ? (
                      <ErrorNote compact>
                        {errors[`close:${session.id}`]}
                      </ErrorNote>
                    ) : null}
                  </td>
                  <td className="px-3 py-4">
                    <StatusPill tone={session.anchored ? "held" : "ready"}>
                      {session.anchored ? "Held by command" : "Running"}
                    </StatusPill>
                  </td>
                  <td className="px-3 py-4 text-xs text-muted-foreground">
                    {formatTime(session.startedAt)}
                  </td>
                  <td className="px-3 py-4">
                    <div className="flex justify-end gap-2">
                      <LinkButton
                        to="/sandboxes/$id"
                        params={{ id: session.id }}
                      >
                        Open
                      </LinkButton>
                      {!session.anchored ? (
                        <CloseSandboxButton
                          label="Close"
                          pending={pending === `close:${session.id}`}
                          disabled={pending !== null}
                          onClose={() => void onClose(session.id)}
                        />
                      ) : null}
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </DataTable>
        )}
      </TabPanel>
    </ConsoleFrame>
  )
}
