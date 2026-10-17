import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { useEffect, useState, type ReactNode } from "react"
import { ErrorNote, LoadingLine } from "@/components/feedback"
import { LinkButton, StatefulButton } from "@/components/ui/button"
import {
  ColumnHeading,
  ConsoleFrame,
  DataTable,
} from "@/components/console-frame"
import { CopyButton } from "@/components/copy-button"
import { OpenInChat } from "@/components/open-in-chat"
import { RequestLog } from "@/components/request-log"
import { ScenarioChips } from "@/components/scenario-chip"
import { ServiceIdentity } from "@/components/service-identity"
import { StatusPill } from "@/components/status-pill"
import { closeSandbox, getSession } from "@/lib/api"
import { formatTime, kindLabel } from "@/lib/format"
import { isLoopbackURL } from "@/lib/publish-session"
import type { Session } from "@/lib/types"

export const Route = createFileRoute("/sandboxes/$id")({
  component: SandboxPage,
})

function SandboxPage() {
  const { id } = Route.useParams()
  const navigate = useNavigate()
  const [session, setSession] = useState<Session | null>(null)
  const [missing, setMissing] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [closing, setClosing] = useState(false)

  useEffect(() => {
    let cancelled = false
    const tick = () => {
      getSession(id)
        .then((next) => {
          if (cancelled) return
          setSession(next)
          setMissing(false)
          setError(null)
        })
        .catch((loadError: unknown) => {
          if (cancelled) return
          setMissing(true)
          setSession(null)
          setError(
            loadError instanceof Error
              ? loadError.message
              : "Could not load sandbox"
          )
        })
    }
    tick()
    const timer = window.setInterval(tick, 2000)
    return () => {
      cancelled = true
      window.clearInterval(timer)
    }
  }, [id])

  async function close() {
    setClosing(true)
    try {
      await closeSandbox(id)
      await navigate({ to: "/" })
    } catch (closeError: unknown) {
      setError(
        closeError instanceof Error ? closeError.message : "Could not close"
      )
      setClosing(false)
    }
  }

  if (session === null && !missing) {
    return (
      <ConsoleFrame crumbs={[{ label: "Sandboxes", to: "/" }, { label: id }]}>
        <LoadingLine>Loading sandbox…</LoadingLine>
      </ConsoleFrame>
    )
  }
  if (missing || session === null) {
    return (
      <ConsoleFrame
        crumbs={[{ label: "Sandboxes", to: "/" }, { label: "Closed" }]}
        eyebrow="Sandbox"
        heading="Sandbox closed"
        description={error ?? "This sandbox is not running."}
        actions={<LinkButton to="/">Back to sandboxes</LinkButton>}
      >
        {null}
      </ConsoleFrame>
    )
  }

  return (
    <SandboxDetail
      session={session}
      closing={closing}
      error={error}
      onClose={() => void close()}
    />
  )
}

function contextAction(session: Session): ReactNode {
  switch (session.kind) {
    case "environment":
      return (
        <LinkButton to="/environments/$name" params={{ name: session.name }}>
          Environment
        </LinkButton>
      )
    case "scenario": {
      const scenario = session.services[0]?.scenario
      if (!scenario) return null
      return (
        <LinkButton to="/scenarios/$id" params={{ id: scenario }}>
          Explore scenario
        </LinkButton>
      )
    }
    default: {
      const neverKind: never = session.kind
      return neverKind
    }
  }
}

function reachability(session: Session) {
  const url = session.services.find((service) => service.url)?.url ?? ""
  if (isLoopbackURL(url)) {
    return "This sandbox is on loopback. A process on another machine cannot reach it."
  }
  return "Service URLs use the console host, so a client that can open the console can call them."
}

function SandboxDetail({
  session,
  closing,
  error,
  onClose,
}: {
  session: Session
  closing: boolean
  error: string | null
  onClose: () => void
}) {
  return (
    <ConsoleFrame
      crumbs={[{ label: "Sandboxes", to: "/" }, { label: session.label }]}
      eyebrow="Sandbox"
      heading={session.label}
      description={`${session.name} · ${kindLabel(session.kind)} · session ${session.id}. Started ${formatTime(session.startedAt)}. ${reachability(session)}`}
      actions={
        <div className="flex flex-wrap items-center gap-2">
          <OpenInChat
            query={session.agentPrompt}
            subject={`Fabricate sandbox ${session.label}`}
          />
          {contextAction(session)}
          {session.anchored ? (
            <p className="max-w-sm text-sm text-muted-foreground">
              Held by fab run. It stays up until that command exits.
            </p>
          ) : (
            <StatefulButton
              type="button"
              variant="outline"
              size="sm"
              state={closing ? "loading" : "idle"}
              loadingText="Closing…"
              onClick={onClose}
            >
              Close sandbox
            </StatefulButton>
          )}
        </div>
      }
      sections={[
        { id: "scenarios", label: "Scenarios" },
        { id: "services", label: "Services" },
        { id: "connect", label: "Connect" },
        { id: "log", label: "Log" },
      ]}
    >
      <section id="scenarios" className="flex scroll-mt-6 flex-col gap-3">
        <h2 className="text-lg font-medium">Active scenarios</h2>
        <ScenarioChips services={session.services} />
      </section>
      <div className="flex flex-wrap items-center gap-2">
        <StatusPill tone={session.anchored ? "held" : "ready"}>
          {session.anchored ? "Held by fab run" : "Ready"}
        </StatusPill>
      </div>
      {error ? <ErrorNote>{error}</ErrorNote> : null}

      <section id="services" className="flex scroll-mt-6 flex-col gap-3">
        <h2 className="text-lg font-medium">Services</h2>
        <DataTable>
          <thead>
            <tr>
              <ColumnHeading>Service</ColumnHeading>
              <ColumnHeading>URL</ColumnHeading>
              <ColumnHeading>Token</ColumnHeading>
              <ColumnHeading>Production hostnames</ColumnHeading>
            </tr>
          </thead>
          <tbody>
            {session.services.map((service) => (
              <tr key={service.name} className="border-t align-top">
                <td className="px-3 py-3">
                  <ServiceIdentity
                    resource={service.resource}
                    displayName={service.displayName}
                    scenario={service.scenario}
                    detail={service.name}
                  />
                </td>
                <td className="px-3 py-3">
                  <p className="text-[11px] tracking-wide text-faint uppercase">
                    {service.urlVariable}
                  </p>
                  <p className="max-w-sm font-mono text-xs break-all">
                    {service.url}
                  </p>
                  <div className="mt-2">
                    <CopyButton value={service.url} label="Copy URL" />
                  </div>
                </td>
                <td className="px-3 py-3">
                  <p className="text-[11px] tracking-wide text-faint uppercase">
                    {service.tokenVariable}
                  </p>
                  <p className="max-w-xs font-mono text-xs break-all">
                    {service.token}
                  </p>
                  <div className="mt-2">
                    <CopyButton value={service.token} label="Copy token" />
                  </div>
                </td>
                <td className="px-3 py-3 text-muted-foreground">
                  {service.providerHosts.join(", ") || "None"}
                </td>
              </tr>
            ))}
          </tbody>
        </DataTable>
      </section>

      <section id="connect" className="flex scroll-mt-6 flex-col gap-3">
        <div className="flex items-center justify-between gap-3">
          <h2 className="text-lg font-medium">Connect</h2>
          <CopyButton value={session.connectShell} label="Copy shell" />
        </div>
        <p className="max-w-3xl text-sm leading-6 text-muted-foreground">
          {session.proxy
            ? "This console is in proxy mode, so the sandbox keeps production hostnames. Export these variables into the process under test. It must honor HTTP_PROXY and the CA file. Do not send the service token on routed hosts; the proxy replaces Authorization."
            : "This console publishes each service URL. Point the client at that URL and send the bearer token. Append the provider path your client already calls."}
        </p>
        <pre className="overflow-auto border bg-code p-4 font-mono text-xs leading-5 whitespace-pre-wrap">
          {session.connectShell}
        </pre>
      </section>

      <section id="log" className="flex scroll-mt-6 flex-col gap-3 text-sm">
        <div className="flex items-center justify-between gap-3">
          <h2 className="text-lg font-medium">Log</h2>
          <CopyButton value={session.requestLog} label="Copy log path" />
        </div>
        <p className="font-mono text-xs break-all text-muted-foreground">
          {session.requestLog}
        </p>
        <RequestLog id={session.id} />
      </section>
    </ConsoleFrame>
  )
}
