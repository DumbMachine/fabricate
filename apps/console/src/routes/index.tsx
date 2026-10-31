import { Link, createFileRoute, useNavigate } from "@tanstack/react-router"
import { useMemo, useState } from "react"
import { Boxes, ChevronRight, Plus } from "lucide-react"
import {
  ConsoleFrame,
  ColumnHeading,
  DataTable,
} from "@/components/console-frame"
import { ErrorNote, LoadingLine } from "@/components/feedback"
import { ResourceIcon } from "@/components/resource-icon"
import { ResourceSheet } from "@/components/resource-sheet"
import { StatusPill } from "@/components/status-pill"
import { Button, LinkButton } from "@/components/ui/button"
import { CloseSandboxButton } from "@/components/close-sandbox"
import { PageTabs, TabPanel } from "@/components/page-tabs"
import { matchingResources } from "@/lib/catalog"
import { formatTime, matchesQuery } from "@/lib/format"
import { useCatalog, useSessions, useSandboxActions } from "@/lib/use-console"
export const Route = createFileRoute("/")({
  validateSearch: (search: Record<string, unknown>): { tab?: "resources" } =>
    search.tab === "resources" ? { tab: "resources" } : {},
  component: SandboxesPage,
})
function SandboxesPage() {
  const { catalog, error: catalogError } = useCatalog()
  const [sessions, setSessions, sessionsError] = useSessions()
  const { pending, errors, start, close } = useSandboxActions()
  const [query, setQuery] = useState("")
  const [resourceId, setResourceId] = useState<string | null>(null)
  const { tab } = Route.useSearch()
  const navigate = useNavigate()
  const active = tab || "running"
  const changeTab = (next: string) => {
    setQuery("")
    void navigate({
      to: "/",
      search: next === "resources" ? { tab: "resources" } : {},
    })
  }
  const visible = useMemo(
    () =>
      (sessions || []).filter((s) =>
        matchesQuery(query, [
          s.label,
          s.name,
          s.id,
          ...s.services.flatMap((v) => [v.displayName, v.name, v.scenario]),
        ])
      ),
    [sessions, query]
  )
  const resources = useMemo(
    () => matchingResources(catalog?.scenarios || [], query),
    [catalog, query]
  )
  const allResources = useMemo(
    () => matchingResources(catalog?.scenarios || [], ""),
    [catalog]
  )
  const selected = resources.find((v) => v.resource === resourceId) || null
  async function onClose(id: string) {
    if (await close(id))
      setSessions((current) => (current || []).filter((s) => s.id !== id))
  }
  return (
    <ConsoleFrame
      crumbs={[{ label: "Sandboxes" }]}
      actions={
        <Button size="sm" onClick={() => changeTab("resources")}>
          <Plus />
          New sandbox
        </Button>
      }
      tabs={
        <PageTabs
          id="sandboxes"
          label="Sandbox views"
          value={active}
          onChange={changeTab}
          items={[
            { id: "running", label: "Running", count: sessions?.length },
            {
              id: "resources",
              label: "Resource catalog",
              count: catalog ? allResources.length : undefined,
            },
          ]}
        />
      }
      search={{
        value: query,
        onChange: setQuery,
        label:
          active === "running"
            ? "Search sandboxes"
            : "Search resources and scenarios",
      }}
    >
      <TabPanel id="sandboxes" value={active}>
        {active === "running" ? (
          <>
            {sessionsError ? <ErrorNote>{sessionsError}</ErrorNote> : null}
            {sessions === null && !sessionsError ? (
              <LoadingLine>Loading sandboxes…</LoadingLine>
            ) : null}
            {sessions && visible.length === 0 ? (
              <div className="flex min-h-72 flex-col items-center justify-center gap-3 text-center">
                <Boxes className="mb-1 size-8 text-faint" />
                <h2 className="text-base font-medium">
                  {query
                    ? "No matching sandboxes"
                    : "Your sandboxes will appear here"}
                </h2>
                <p className="max-w-sm text-[13px] leading-6 text-muted-foreground">
                  {query
                    ? "Try a resource name, scenario or sandbox ID."
                    : "Choose a resource and its starting data, or bring up a complete environment."}
                </p>
                <Button
                  size="sm"
                  variant="outline"
                  className="mt-2"
                  onClick={() => changeTab("resources")}
                >
                  Browse resources
                </Button>
              </div>
            ) : null}
            {visible.length ? (
              <DataTable>
                <thead>
                  <tr>
                    <ColumnHeading>Sandbox</ColumnHeading>
                    <ColumnHeading>Services</ColumnHeading>
                    <ColumnHeading>Status</ColumnHeading>
                    <ColumnHeading>Started</ColumnHeading>
                    <ColumnHeading>Actions</ColumnHeading>
                  </tr>
                </thead>
                <tbody>
                  {visible.map((s) => (
                    <tr key={s.id} className="border-b align-middle">
                      <td className="px-3 py-4">
                        <Link
                          to="/sandboxes/$id"
                          params={{ id: s.id }}
                          className="font-medium hover:underline"
                        >
                          {s.label}
                        </Link>
                        <p className="mt-1 font-mono text-[10px] text-faint">
                          {s.id}
                        </p>
                        {errors[`close:${s.id}`] ? (
                          <ErrorNote compact>
                            {errors[`close:${s.id}`]}
                          </ErrorNote>
                        ) : null}
                      </td>
                      <td className="px-3 py-4">
                        <div className="flex items-center gap-2">
                          {s.services.slice(0, 4).map((service) => (
                            <ResourceIcon
                              key={service.name}
                              id={service.resource}
                            />
                          ))}
                          <span className="text-xs text-muted-foreground">
                            {s.services.length}{" "}
                            {s.services.length === 1 ? "service" : "services"}
                          </span>
                        </div>
                      </td>
                      <td className="px-3 py-4">
                        <StatusPill tone={s.anchored ? "held" : "ready"}>
                          {s.anchored ? "Held by command" : "Running"}
                        </StatusPill>
                      </td>
                      <td className="px-3 py-4 text-xs text-muted-foreground">
                        {formatTime(s.startedAt)}
                      </td>
                      <td className="px-3 py-4">
                        <div className="flex justify-end gap-2">
                          <LinkButton to="/sandboxes/$id" params={{ id: s.id }}>
                            Open
                          </LinkButton>
                          {!s.anchored ? (
                            <CloseSandboxButton
                              label="Close"
                              onClose={() => void onClose(s.id)}
                              pending={pending === `close:${s.id}`}
                              disabled={pending !== null}
                            />
                          ) : null}
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </DataTable>
            ) : null}
          </>
        ) : (
          <>
            {catalogError ? <ErrorNote>{catalogError}</ErrorNote> : null}
            {!catalog && !catalogError ? (
              <LoadingLine>Loading resources…</LoadingLine>
            ) : null}
            <p className="text-[13px] text-muted-foreground">
              Choose a service to explore its data and start an isolated
              sandbox.
            </p>
            {catalog && !resources.length ? (
              <p className="py-12 text-center text-sm text-muted-foreground">
                No resources match your search.
              </p>
            ) : null}
            <div className="grid grid-cols-1 gap-x-7 sm:grid-cols-2 xl:grid-cols-3">
              {resources.map((resource) => (
                <button
                  type="button"
                  key={resource.resource}
                  aria-haspopup="dialog"
                  aria-expanded={selected?.resource === resource.resource}
                  onClick={() => setResourceId(resource.resource)}
                  className="group flex min-h-20 items-center gap-3 border-b border-border px-2 py-4 text-left outline-none hover:bg-accent/50 focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-inset"
                >
                  <ResourceIcon id={resource.resource} size="md" />
                  <span className="min-w-0 flex-1">
                    <span className="block truncate text-[13px] font-medium">
                      {resource.displayName}
                    </span>
                    <span className="mt-1 block text-xs text-muted-foreground">
                      {resource.scenarios.length}{" "}
                      {resource.scenarios.length === 1
                        ? "scenario"
                        : "scenarios"}
                    </span>
                  </span>
                  <ChevronRight className="size-3.5 text-faint" />
                </button>
              ))}
            </div>
          </>
        )}
      </TabPanel>
      <ResourceSheet
        resource={selected}
        sessions={sessions}
        open={selected !== null}
        pending={pending}
        errors={errors}
        onOpenChange={(open) => {
          if (!open) setResourceId(null)
        }}
        onStart={(id) => void start("scenario", id, "sandbox")}
      />
    </ConsoleFrame>
  )
}
