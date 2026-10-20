import { createFileRoute } from "@tanstack/react-router"
import { useEffect, useState } from "react"
import { AgentCopy } from "@/components/agent-copy"
import { ErrorNote, LoadingLine } from "@/components/feedback"
import { ResourceIcon } from "@/components/resource-icon"
import { StartActions } from "@/components/start-actions"
import {
  ColumnHeading,
  ConsoleFrame,
  DataTable,
  type FrameSection,
} from "@/components/console-frame"
import { scenarioAgentBrief } from "@/lib/agent-brief"
import { getScenario } from "@/lib/api"
import { collectionSectionId, collectionTitle, errorText } from "@/lib/format"
import { useSandboxActions, useSessions } from "@/lib/use-console"
import type { ScenarioCollection, ScenarioView } from "@/lib/types"

export const Route = createFileRoute("/scenarios/$id")({
  component: ScenarioPage,
})

function ScenarioPage() {
  const { id } = Route.useParams()
  const [view, setView] = useState<ScenarioView | null>(null)
  const [error, setError] = useState<string | null>(null)
  const { pending, errors, start } = useSandboxActions()
  const [sessions] = useSessions()

  useEffect(() => {
    let cancelled = false
    setView(null)
    setError(null)
    getScenario(id)
      .then((next) => {
        if (!cancelled) setView(next)
      })
      .catch((loadError: unknown) => {
        if (!cancelled) setError(errorText(loadError))
      })
    return () => {
      cancelled = true
    }
  }, [id])

  async function onStart() {
    await start("scenario", id, "sandbox")
  }

  if (view === null && error === null) {
    return (
      <ConsoleFrame crumbs={[{ label: "Sandboxes", to: "/" }, { label: id }]}>
        <LoadingLine>Loading scenario…</LoadingLine>
      </ConsoleFrame>
    )
  }
  if (view === null) {
    return (
      <ConsoleFrame
        crumbs={[{ label: "Sandboxes", to: "/" }, { label: "Unknown" }]}
        eyebrow="Scenario"
        heading="Scenario not found"
        description={error ?? "This scenario is not in the catalog."}
      >
        {null}
      </ConsoleFrame>
    )
  }

  const sections: FrameSection[] = [
    ...(view.scalars.length > 0 ? [{ id: "summary", label: "Summary" }] : []),
    ...view.collections.map((collection, index) => ({
      id: collectionSectionId(collection.key, index),
      label: collectionTitle(collection.key),
    })),
  ]
  const key = `scenario:${view.id}`

  return (
    <ConsoleFrame
      crumbs={[{ label: "Sandboxes", to: "/" }, { label: view.name }]}
      eyebrow={view.displayName}
      heading={view.name}
      description={`${view.description} ${view.id} · ${view.resourceVersion}. ${
        view.providerHosts.join(", ") || "No production hostnames"
      }. This is the seeded state a sandbox loads for this scenario.`}
      actions={
        <div className="flex flex-wrap items-end gap-2">
          <AgentCopy brief={scenarioAgentBrief(view, sessions)} />
          <StartActions
            pending={pending}
            actionKey={key}
            onStart={() => void onStart()}
            label="Start scenario"
          />
        </div>
      }
      sections={sections}
    >
      <div className="flex items-center gap-3">
        <ResourceIcon id={view.resource} size="md" />
        <div>
          <p className="font-medium">{view.displayName}</p>
          <p className="font-mono text-xs text-muted-foreground">{view.id}</p>
        </div>
      </div>
      {errors[key] ? <ErrorNote>{errors[key]}</ErrorNote> : null}

      {view.scalars.length > 0 ? (
        <section id="summary" className="flex scroll-mt-6 flex-col gap-3">
          <h2 className="text-lg font-medium">Summary</h2>
          <dl className="grid gap-3 sm:grid-cols-2">
            {view.scalars.map((scalar) => (
              <div key={scalar.key} className="border bg-card px-3 py-2">
                <dt className="text-[11px] font-medium tracking-wider text-faint uppercase">
                  {scalar.key}
                </dt>
                <dd className="mt-1 font-mono text-sm break-all">
                  {scalar.value}
                </dd>
              </div>
            ))}
          </dl>
        </section>
      ) : null}

      {view.collections.map((collection, index) => (
        <CollectionSection
          key={collection.key}
          collection={collection}
          index={index}
        />
      ))}
    </ConsoleFrame>
  )
}

function CollectionSection({
  collection,
  index,
}: {
  collection: ScenarioCollection
  index: number
}) {
  return (
    <section
      id={collectionSectionId(collection.key, index)}
      className="flex scroll-mt-6 flex-col gap-3"
    >
      <div className="flex items-baseline justify-between gap-3">
        <h2 className="text-lg font-medium">
          {collectionTitle(collection.key)}
        </h2>
        <p className="text-xs text-muted-foreground">
          {collection.shown < collection.total
            ? `Showing ${collection.shown} of ${collection.total} rows`
            : `${collection.total} ${collection.total === 1 ? "row" : "rows"}`}
        </p>
      </div>
      {collection.total === 0 || collection.columns.length === 0 ? (
        <p className="text-sm text-muted-foreground">No rows.</p>
      ) : (
        <DataTable className="min-w-[960px]">
          <thead>
            <tr>
              {collection.columns.map((column) => (
                <ColumnHeading key={column}>{column}</ColumnHeading>
              ))}
            </tr>
          </thead>
          <tbody>
            {collection.rows.map((row, rowIndex) => (
              <tr
                key={`${collection.key}-${rowIndex}`}
                className="border-t align-top"
              >
                {collection.columns.map((column) => (
                  <td
                    key={column}
                    className="max-w-72 px-3 py-2 text-xs break-words"
                  >
                    {row[column]}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </DataTable>
      )}
    </section>
  )
}
