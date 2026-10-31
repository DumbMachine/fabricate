import { createFileRoute } from "@tanstack/react-router"
import { useEffect, useState } from "react"
import { CheckCircle2, Server, RefreshCw } from "lucide-react"
import { ConsoleFrame } from "@/components/console-frame"
import { PageTabs, TabPanel } from "@/components/page-tabs"
import { CopyButton } from "@/components/copy-button"
import { ErrorNote, LoadingLine } from "@/components/feedback"
import { Button } from "@/components/ui/button"
import { useConsoleAuth } from "@/components/auth-gate"
import { getConfig } from "@/lib/api"
import type { ConsoleConfig } from "@/lib/types"
export const Route = createFileRoute("/deployment")({
  component: DeploymentPage,
})
function DeploymentPage() {
  const [tab, setTab] = useState("overview"),
    [config, setConfig] = useState<ConsoleConfig | null>(null),
    [error, setError] = useState(""),
    [checking, setChecking] = useState(false)
  const auth = useConsoleAuth()
  const origin = config?.publicUrl || window.location.origin
  const refresh = async () => {
    setChecking(true)
    setError("")
    try {
      const next = await getConfig()
      const health = await fetch("/healthz")
      if (!health.ok) throw new Error("Deployment health check failed")
      setConfig(next)
    } catch (e) {
      setError(e instanceof Error ? e.message : "Cannot reach deployment")
    } finally {
      setChecking(false)
    }
  }
  useEffect(() => {
    void refresh()
  }, [])
  const fields = [
    ["Connection", error ? "Last check failed" : "Reachable"],
    ["Advertised origin", origin],
    [
      "Service routing",
      config?.proxy ? "Provider hostname proxy" : "Direct service URLs",
    ],
    [
      "Operator access",
      auth?.required
        ? "Token protected · 8-hour browser sessions"
        : "Not protected",
    ],
    [
      "Sandbox storage",
      config?.durable
        ? "Persistent · recovers after restart"
        : "Disposable · until this process stops",
    ],
  ]
  const example = `curl -H "Authorization: Bearer $FABRICATE_TOKEN" \
  ${origin}/.well-known/fabricate`
  return (
    <ConsoleFrame
      crumbs={[{ label: "Deployment" }]}
      actions={
        <Button
          variant="outline"
          size="sm"
          disabled={checking}
          onClick={() => void refresh()}
        >
          <RefreshCw className={checking ? "animate-spin" : ""} />
          Check connection
        </Button>
      }
      tabs={
        <PageTabs
          id="deployment"
          label="Deployment views"
          value={tab}
          onChange={setTab}
          items={[
            { id: "overview", label: "Overview" },
            { id: "api", label: "API access" },
          ]}
        />
      }
    >
      <TabPanel id="deployment" value={tab}>
        {error ? <ErrorNote>{error}</ErrorNote> : null}
        {!config && !error ? (
          <LoadingLine>Checking deployment…</LoadingLine>
        ) : null}
        {tab === "overview" && config ? (
          <>
            <div className="flex items-center gap-3 py-2">
              <span className="grid size-10 place-items-center rounded-lg border">
                <Server className="size-4 text-muted-foreground" />
              </span>
              <div>
                <h2 className="text-sm font-medium">Fabricate deployment</h2>
                <p className="mt-1 flex items-center gap-1.5 text-xs text-muted-foreground">
                  <CheckCircle2 className="size-3 text-success" />
                  {error
                    ? "Showing last successful configuration"
                    : "Console and control API connected"}
                </p>
              </div>
            </div>
            <dl className="max-w-3xl divide-y divide-border">
              {fields.map(([label, value]) => (
                <div
                  key={label}
                  className="grid gap-2 py-4 text-[13px] sm:grid-cols-[180px_1fr]"
                >
                  <dt className="text-muted-foreground">{label}</dt>
                  <dd className="break-all">{value}</dd>
                </div>
              ))}
            </dl>
            <p className="max-w-2xl text-xs leading-6 text-muted-foreground">
              {config.durable
                ? "Sandbox URLs, service tokens and data survive deployment restarts. Closing a sandbox deletes its stored data."
                : "Request logs are retained by the deployment. Sandbox data is discarded when this process stops."}
            </p>
            {auth?.required ? (
              <div className="pt-3">
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() =>
                    void auth
                      .logout()
                      .catch(() => setError("Could not sign out. Try again."))
                  }
                >
                  Sign out
                </Button>
              </div>
            ) : null}
          </>
        ) : tab === "api" ? (
          <>
            <h2 className="text-sm font-medium">
              Connect Access or another client
            </h2>
            <p className="max-w-2xl text-[13px] leading-6 text-muted-foreground">
              Use your operator token for environment creation and discovery.
              Use each sandbox’s service token for provider calls. The console’s
              browser session stays in this browser.
            </p>
            <div className="flex items-center justify-between gap-3 border-b pb-3 text-[13px]">
              <span>Discovery endpoint</span>
              <CopyButton
                value={`${origin}/.well-known/fabricate`}
                label="Copy URL"
              />
            </div>
            <pre className="overflow-auto rounded-lg border bg-code p-4 text-xs leading-6">
              {example}
            </pre>
            <p className="max-w-2xl text-[13px] leading-6 text-muted-foreground">
              Create a sandbox, then open its Connect tab for service URLs and
              OpenAPI documents. Access can register them with its generic HTTP
              API connector.
            </p>
            <a
              className="text-[13px] underline underline-offset-4"
              href="https://fabricate.dmach.in/docs"
              target="_blank"
              rel="noreferrer"
            >
              Read the integration guide
            </a>
          </>
        ) : null}
      </TabPanel>
    </ConsoleFrame>
  )
}
