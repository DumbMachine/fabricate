import { APIConnection } from "@/components/api-connection"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { useEffect, useState } from "react"
import {
  ConsoleFrame,
  DataTable,
  ColumnHeading,
} from "@/components/console-frame"
import { PageTabs, TabPanel } from "@/components/page-tabs"
import { CloseSandboxButton } from "@/components/close-sandbox"
import { CopyButton } from "@/components/copy-button"
import { AgentCopy } from "@/components/agent-copy"
import { SecretValue } from "@/components/secret-value"
import { ErrorNote, LoadingLine } from "@/components/feedback"
import { LinkButton } from "@/components/ui/button"
import { RequestLog } from "@/components/request-log"
import { ServiceIdentity } from "@/components/service-identity"
import { StatusPill } from "@/components/status-pill"
import { sandboxAgentBrief } from "@/lib/agent-brief"
import { ApiError, closeSandbox, getSession } from "@/lib/api"
import { formatTime, kindLabel } from "@/lib/format"
import type { Session } from "@/lib/types"
export const Route = createFileRoute("/sandboxes/$id")({
  component: SandboxPage,
})
function SandboxPage() {
  const { id } = Route.useParams()
  const navigate = useNavigate()
  const [session, setSession] = useState<Session | null>(null),
    [missing, setMissing] = useState(false),
    [error, setError] = useState<string | null>(null),
    [closing, setClosing] = useState(false),
    [tab, setTab] = useState("services"),
    [includeCredentials, setIncludeCredentials] = useState(false)
  useEffect(() => {
    setSession(null)
    setMissing(false)
    setError(null)
    setTab("services")
    setIncludeCredentials(false)
    let cancelled = false
    let timer: ReturnType<typeof setTimeout>
    const tick = async () => {
      try {
        const next = await getSession(id)
        if (!cancelled) {
          setSession(next)
          setMissing(false)
          setError(null)
        }
      } catch (e) {
        if (!cancelled) {
          setError(e instanceof Error ? e.message : "Could not load sandbox")
          if (e instanceof ApiError && e.status === 404) {
            setSession(null)
            setMissing(true)
          }
        }
      } finally {
        if (!cancelled) timer = setTimeout(() => void tick(), 4000)
      }
    }
    void tick()
    return () => {
      cancelled = true
      clearTimeout(timer)
    }
  }, [id])
  const close = async () => {
    if (closing) return
    setClosing(true)
    try {
      await closeSandbox(id)
      await navigate({ to: "/" })
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not close sandbox")
      setClosing(false)
    }
  }
  if (!session)
    return (
      <ConsoleFrame
        crumbs={[
          { label: "Sandboxes", to: "/" },
          { label: missing ? "Closed" : id },
        ]}
        actions={
          missing ? (
            <LinkButton to="/">Back to sandboxes</LinkButton>
          ) : undefined
        }
      >
        {missing ? (
          <p className="py-12 text-sm text-muted-foreground">
            This sandbox has been closed. Its services are no longer available.
          </p>
        ) : error ? (
          <ErrorNote>{error}</ErrorNote>
        ) : (
          <LoadingLine>Loading sandbox…</LoadingLine>
        )}
      </ConsoleFrame>
    )
  let shell = session.connectShell
  if (!includeCredentials)
    for (const service of session.services)
      shell = shell.split(service.token).join("<service-token>")
  return (
    <ConsoleFrame
      crumbs={[{ label: "Sandboxes", to: "/" }, { label: session.label }]}
      actions={
        !session.anchored ? (
          <CloseSandboxButton
            onClose={() => void close()}
            pending={closing}
            disabled={closing}
          />
        ) : undefined
      }
      tabs={
        <PageTabs
          id="sandbox"
          label="Sandbox views"
          value={tab}
          onChange={setTab}
          items={[
            {
              id: "services",
              label: "Services",
              count: session.services.length,
            },
            { id: "connect", label: "Connect" },
            { id: "activity", label: "Activity" },
          ]}
        />
      }
    >
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2 text-xs text-muted-foreground">
        <StatusPill tone={session.anchored ? "held" : "ready"}>
          {session.anchored ? "Held by command" : "Running"}
        </StatusPill>
        <span>{kindLabel(session.kind)}</span>
        <span className="font-mono text-[11px]">{session.id}</span>
        <span>Started {formatTime(session.startedAt)}</span>
        {session.kind === "environment" ? (
          <LinkButton to="/environments/$name" params={{ name: session.name }}>
            View environment
          </LinkButton>
        ) : null}
      </div>
      {error ? <ErrorNote>{error}</ErrorNote> : null}
      <TabPanel id="sandbox" value={tab}>
        {tab === "services" ? (
          <>
            <p className="text-[13px] leading-6 text-muted-foreground">
              Each service has isolated state and its own access token.
            </p>
            <DataTable>
              <thead>
                <tr>
                  <ColumnHeading>Service and scenario</ColumnHeading>
                  <ColumnHeading>Endpoint</ColumnHeading>
                  <ColumnHeading>Service token</ColumnHeading>
                </tr>
              </thead>
              <tbody>
                {session.services.map((service) => (
                  <tr key={service.name} className="border-b align-top">
                    <td className="px-3 py-5">
                      <ServiceIdentity
                        resource={service.resource}
                        displayName={service.displayName}
                        scenario={service.scenario}
                        detail={service.name}
                      />
                    </td>
                    <td className="px-3 py-5">
                      <code className="block max-w-sm text-xs leading-5 break-all text-muted-foreground">
                        {service.url}
                      </code>
                      <div className="mt-2">
                        <CopyButton value={service.url} label="Copy URL" />
                      </div>
                    </td>
                    <td className="px-3 py-5">
                      <SecretValue value={service.token} />
                    </td>
                  </tr>
                ))}
              </tbody>
            </DataTable>
          </>
        ) : tab === "connect" ? (
          <>
            <h2 className="text-sm font-medium">Connect an app or agent</h2>
            <p className="max-w-3xl text-[13px] leading-6 text-muted-foreground">
              {session.proxy
                ? "Use the proxy URL and CA file in the client process to keep the provider’s normal hostname. The proxy supplies the sandbox’s service credential."
                : "Use a service endpoint and its token in your app’s API connection. Copy the API base URL and OpenAPI document below to configure a connection."}
            </p>
            <div className="flex flex-wrap items-center justify-between gap-3">
              <label className="flex items-center gap-2 text-xs text-muted-foreground">
                <input
                  type="checkbox"
                  checked={includeCredentials}
                  onChange={(e) => setIncludeCredentials(e.target.checked)}
                />
                Include service credentials
              </label>
              <CopyButton value={shell} label="Copy environment variables" />
            </div>
            <pre className="overflow-auto rounded-lg border bg-code p-4 font-mono text-xs leading-6 whitespace-pre-wrap">
              {shell}
            </pre>
            <div>
              {session.services.map((service) => (
                <APIConnection key={service.name} service={service} />
              ))}
            </div>
            <div className="border-t pt-5">
              <AgentCopy brief={sandboxAgentBrief(session)} />
            </div>
          </>
        ) : (
          <RequestLog id={session.id} />
        )}
      </TabPanel>
    </ConsoleFrame>
  )
}
