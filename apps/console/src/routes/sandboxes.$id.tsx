import { Link, createFileRoute, useNavigate } from "@tanstack/react-router"
import { useEffect, useState, type ReactNode } from "react"
import { Button } from "@/components/ui/button"
import {
  ColumnHeading,
  ConsoleFrame,
  DataTable,
  FilterTabs,
} from "@/components/console-frame"
import { CopyButton } from "@/components/copy-button"
import { ScenarioChips } from "@/components/scenario-chip"
import { ServiceIdentity } from "@/components/service-identity"
import { StatusPill } from "@/components/status-pill"
import { closeSandbox, getSession } from "@/lib/api"
import { agents, formatTime, kindLabel, withAgent } from "@/lib/format"
import type { Agent } from "@/lib/format"
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
  const [agent, setAgent] = useState<Agent>("Cursor")

  useEffect(() => {
    let cancelled = false
    const tick = () => {
      getSession(id)
        .then((next) => {
          if (cancelled) return
          setSession(forBrowser(next))
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
        <p className="text-sm text-muted-foreground">Loading sandbox…</p>
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
        actions={
          <Button render={<Link to="/" />} variant="outline">
            Back to sandboxes
          </Button>
        }
      >
        {null}
      </ConsoleFrame>
    )
  }

  return (
    <SandboxDetail
      session={session}
      agent={agent}
      onAgent={setAgent}
      closing={closing}
      error={error}
      onClose={() => void close()}
    />
  )
}

const loopbackNote =
  "The sandbox binds to loopback, so only a process on this machine can reach it."
const sharedHostNote =
  "These service URLs are on this console host. A client that can open the console can call them with the bearer token."

function forBrowser(session: Session): Session {
  if (typeof window === "undefined") {
    return session
  }
  const origin = window.location.origin
  let connectShell = session.connectShell
  let agentPrompt = session.agentPrompt
  const services = session.services.map((service) => {
    if (!isLoopbackURL(service.url)) {
      return service
    }
    const url = `${origin}/s/${session.id}/${service.name}`
    connectShell = connectShell.split(service.url).join(url)
    agentPrompt = agentPrompt.split(service.url).join(url)
    return { ...service, url }
  })
  const variables = session.variables.map((variable) => {
    const service = services.find((item) => item.urlVariable === variable.name)
    if (service === undefined || !isLoopbackURL(variable.value)) {
      return variable
    }
    connectShell = connectShell.split(variable.value).join(service.url)
    agentPrompt = agentPrompt.split(variable.value).join(service.url)
    return { ...variable, value: service.url }
  })
  if (services.some((service) => !isLoopbackURL(service.url))) {
    agentPrompt = agentPrompt.split(loopbackNote).join(sharedHostNote)
  }
  return { ...session, services, variables, connectShell, agentPrompt }
}

function contextAction(session: Session): ReactNode {
  switch (session.kind) {
    case "environment":
      return (
        <Button
          variant="outline"
          render={
            <Link to="/environments/$name" params={{ name: session.name }} />
          }
        >
          Environment
        </Button>
      )
    case "scenario": {
      const scenario = session.services[0]?.scenario
      if (!scenario) return null
      return (
        <Button
          variant="outline"
          render={<Link to="/scenarios/$id" params={{ id: scenario }} />}
        >
          Explore scenario
        </Button>
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
  return "Service URLs use this console host, so you can call them from here."
}

function isLoopbackURL(value: string) {
  try {
    const host = new URL(value).hostname
    return host === "127.0.0.1" || host === "localhost" || host === "::1"
  } catch {
    return false
  }
}

function SandboxDetail({
  session,
  agent,
  onAgent,
  closing,
  error,
  onClose,
}: {
  session: Session
  agent: Agent
  onAgent: (agent: Agent) => void
  closing: boolean
  error: string | null
  onClose: () => void
}) {
  const prompt = withAgent(agent, session.agentPrompt)
  return (
    <ConsoleFrame
      crumbs={[{ label: "Sandboxes", to: "/" }, { label: session.label }]}
      eyebrow="Sandbox"
      heading={session.label}
      description={`${session.name} · ${kindLabel(session.kind)} · session ${session.id}. Started ${formatTime(session.startedAt)}. ${reachability(session)}`}
      actions={
        <div className="flex flex-wrap items-center gap-2">
          {contextAction(session)}
          {session.anchored ? (
            <p className="max-w-sm text-sm text-muted-foreground">
              Held by fab run. It stays up until that command exits.
            </p>
          ) : (
            <Button
              type="button"
              variant="outline"
              disabled={closing}
              onClick={onClose}
            >
              {closing ? "Closing…" : "Close sandbox"}
            </Button>
          )}
        </div>
      }
      sections={[
        { id: "scenarios", label: "Scenarios" },
        { id: "services", label: "Services" },
        { id: "connect", label: "Connect" },
        { id: "agent", label: "Agent" },
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
        <span className="rounded bg-muted px-1.5 py-0.5 text-xs">
          {session.proxy ? "Proxy" : "Direct"}
        </span>
      </div>
      {error ? <p className="text-sm text-destructive">{error}</p> : null}

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
            ? "Proxy mode keeps production hostnames. Export these variables into the process under test. It must honor HTTP_PROXY and the CA file. Do not send the service token on routed hosts; the proxy replaces Authorization."
            : "Direct mode points each client at the service URL and sends the bearer token. Append the provider path your client already calls. Production hostnames still reach the real providers."}
        </p>
        <pre className="overflow-auto border bg-code p-4 font-mono text-xs leading-5 whitespace-pre-wrap">
          {session.connectShell}
        </pre>
      </section>

      <section id="agent" className="flex scroll-mt-6 flex-col gap-3">
        <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <h2 className="text-lg font-medium">Agent</h2>
          <CopyButton value={prompt} label={`Copy for ${agent}`} />
        </div>
        <p className="max-w-3xl text-sm leading-6 text-muted-foreground">
          Paste this prompt into the coding agent that should wire your app. The
          agent gets the sandbox connection. It does not get a separate
          control-plane key.
        </p>
        <FilterTabs
          value={agent}
          onChange={(id) => onAgent(id as Agent)}
          options={agents.map((candidate) => ({
            id: candidate,
            label: candidate,
          }))}
        />
        <pre className="overflow-auto border bg-code p-4 font-mono text-xs leading-5 whitespace-pre-wrap">
          {prompt}
        </pre>
      </section>

      <section id="log" className="flex scroll-mt-6 flex-col gap-2 text-sm">
        <div className="flex items-center justify-between gap-3">
          <h2 className="text-lg font-medium">Log</h2>
          <CopyButton value={session.requestLog} label="Copy log path" />
        </div>
        <p className="font-mono text-xs break-all text-muted-foreground">
          {session.requestLog}
        </p>
      </section>
    </ConsoleFrame>
  )
}
