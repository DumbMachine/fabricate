import { Link, createFileRoute, useNavigate } from "@tanstack/react-router"
import { useEffect, useState } from "react"
import { Button } from "@/components/ui/button"
import { ConsoleHeader } from "@/components/console-header"
import { CopyButton } from "@/components/copy-button"
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

  return (
    <div className="min-h-svh">
      <ConsoleHeader>
        <Link to="/" className="text-sm text-muted-foreground">
          Catalog
        </Link>
      </ConsoleHeader>
      <main className="mx-auto flex max-w-6xl flex-col gap-8 px-6 py-8">
        {session === null && !missing ? (
          <p className="text-sm text-muted-foreground">Loading sandbox…</p>
        ) : null}
        {missing ? (
          <section className="flex flex-col gap-3">
            <h1 className="text-2xl font-medium tracking-tight">
              Sandbox closed
            </h1>
            <p className="text-sm text-muted-foreground">
              {error ?? "This sandbox is not running."}
            </p>
            <Link to="/" className="text-sm font-medium">
              Back to the catalog
            </Link>
          </section>
        ) : null}
        {session ? (
          <SandboxDetail
            session={session}
            agent={agent}
            onAgent={setAgent}
            closing={closing}
            error={error}
            onClose={() => void close()}
          />
        ) : null}
      </main>
    </div>
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
  let rewritten = false
  const services = session.services.map((service) => {
    if (!isLoopbackURL(service.url)) {
      return service
    }
    rewritten = true
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
    rewritten = true
    connectShell = connectShell.split(variable.value).join(service.url)
    agentPrompt = agentPrompt.split(variable.value).join(service.url)
    return { ...variable, value: service.url }
  })
  if (rewritten) {
    agentPrompt = agentPrompt.split(loopbackNote).join(sharedHostNote)
  }
  return { ...session, services, variables, connectShell, agentPrompt }
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
    <>
      <section className="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
        <div className="flex flex-col gap-2">
          <h1 className="text-2xl font-medium tracking-tight">
            {session.label}
          </h1>
          <p className="text-sm text-muted-foreground">
            {session.name} · {kindLabel(session.kind)} · {session.status} ·{" "}
            {session.proxy ? "proxy" : "direct"} · session {session.id}
          </p>
          <p className="text-sm text-muted-foreground">
            Started {formatTime(session.startedAt)}. {reachability(session)}
          </p>
        </div>
        <Button
          type="button"
          variant="destructive"
          disabled={closing}
          onClick={onClose}
        >
          {closing ? "Closing…" : "Close sandbox"}
        </Button>
      </section>
      {error ? <p className="text-sm text-destructive">{error}</p> : null}

      <section className="flex flex-col gap-3">
        <h2 className="text-sm font-medium">Services</h2>
        <ul className="grid gap-3">
          {session.services.map((service) => (
            <li
              key={service.name}
              className="flex flex-col gap-3 rounded-xl border bg-card p-4"
            >
              <div>
                <h3 className="font-medium">
                  {service.displayName}{" "}
                  <span className="font-normal text-muted-foreground">
                    {service.name}
                  </span>
                </h3>
                <p className="font-mono text-xs text-muted-foreground">
                  {service.scenario}
                </p>
              </div>
              <dl className="grid gap-2 text-sm">
                <div>
                  <dt className="text-muted-foreground">
                    {service.urlVariable}
                  </dt>
                  <dd className="font-mono break-all">{service.url}</dd>
                </div>
                <div>
                  <dt className="text-muted-foreground">
                    {service.tokenVariable}
                  </dt>
                  <dd className="font-mono break-all">{service.token}</dd>
                </div>
                <div>
                  <dt className="text-muted-foreground">
                    Production hostnames
                  </dt>
                  <dd>{service.providerHosts.join(", ") || "None"}</dd>
                </div>
              </dl>
              <div className="flex flex-wrap gap-2">
                <CopyButton value={service.url} label="Copy URL" />
                <CopyButton value={service.token} label="Copy token" />
              </div>
            </li>
          ))}
        </ul>
      </section>

      <section className="flex flex-col gap-3">
        <div className="flex items-center justify-between gap-3">
          <h2 className="text-sm font-medium">Connect your app</h2>
          <CopyButton value={session.connectShell} label="Copy shell" />
        </div>
        <p className="max-w-3xl text-sm leading-6 text-muted-foreground">
          {session.proxy
            ? "Proxy mode keeps production hostnames. Export these variables into the process under test. It must honor HTTP_PROXY and the CA file. Do not send the service token on routed hosts; the proxy replaces Authorization."
            : "Direct mode points each client at the service URL and sends the bearer token. Append the provider path your client already calls. Production hostnames still reach the real providers."}
        </p>
        <pre className="overflow-auto rounded-xl bg-muted p-4 text-xs leading-5 whitespace-pre-wrap">
          {session.connectShell}
        </pre>
      </section>

      <section className="flex flex-col gap-3">
        <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <h2 className="text-sm font-medium">Hand off to an agent</h2>
          <CopyButton value={prompt} label={`Copy for ${agent}`} />
        </div>
        <p className="max-w-3xl text-sm leading-6 text-muted-foreground">
          Paste this prompt into the coding agent that should wire your app. The
          agent gets the sandbox connection. It does not get a separate
          control-plane key. Close the sandbox here when the task is done.
        </p>
        <div className="flex flex-wrap gap-2">
          {agents.map((candidate) => (
            <Button
              key={candidate}
              type="button"
              size="sm"
              variant={candidate === agent ? "default" : "outline"}
              onClick={() => onAgent(candidate)}
            >
              {candidate}
            </Button>
          ))}
        </div>
        <pre className="overflow-auto rounded-xl bg-muted p-4 text-xs leading-5 whitespace-pre-wrap">
          {prompt}
        </pre>
      </section>

      <section className="flex flex-col gap-2 text-sm">
        <h2 className="font-medium">Request log</h2>
        <p className="font-mono text-xs break-all text-muted-foreground">
          {session.requestLog}
        </p>
        <div>
          <CopyButton value={session.requestLog} label="Copy log path" />
        </div>
      </section>
    </>
  )
}
