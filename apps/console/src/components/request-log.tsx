import { useEffect, useState } from "react"
import { ColumnHeading, DataTable } from "@/components/console-frame"
import { ErrorNote, LoadingLine } from "@/components/feedback"
import { getSessionLogs } from "@/lib/api"
import { errorText, formatTime } from "@/lib/format"
import type { RequestLogView } from "@/lib/types"

export function RequestLog({ id }: { id: string }) {
  const [view, setView] = useState<RequestLogView | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    const tick = () => {
      getSessionLogs(id)
        .then((next) => {
          if (cancelled) return
          setView(next)
          setError(null)
        })
        .catch((loadError: unknown) => {
          if (cancelled) return
          setError(errorText(loadError))
        })
    }
    tick()
    const timer = window.setInterval(tick, 2000)
    return () => {
      cancelled = true
      window.clearInterval(timer)
    }
  }, [id])

  if (view === null && error === null) {
    return <LoadingLine>Loading log…</LoadingLine>
  }

  return (
    <div className="flex flex-col gap-2">
      <p className="text-sm text-muted-foreground">
        Authorization, cookies, and secret-shaped fields are redacted.
      </p>
      {error ? <ErrorNote>{error}</ErrorNote> : null}
      {view?.truncated ? (
        <p className="text-sm text-muted-foreground">
          Showing the latest {view.entries.length} requests.
        </p>
      ) : null}
      {view && view.entries.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          No requests yet. Calls to this sandbox show up here.
        </p>
      ) : null}
      {view && view.entries.length > 0 ? (
        <DataTable className="min-w-[720px]">
          <thead>
            <tr>
              <ColumnHeading>Time</ColumnHeading>
              <ColumnHeading>Service</ColumnHeading>
              <ColumnHeading>Method</ColumnHeading>
              <ColumnHeading>Path</ColumnHeading>
              <ColumnHeading>Status</ColumnHeading>
            </tr>
          </thead>
          <tbody>
            {view.entries.map((entry, index) => (
              <tr
                key={`${entry.timestamp}-${index}`}
                className="border-t align-top"
              >
                <td className="px-3 py-3 whitespace-nowrap">
                  {formatTime(entry.timestamp)}
                </td>
                <td className="px-3 py-3 font-mono text-xs">{entry.service}</td>
                <td className="px-3 py-3 font-mono text-xs">{entry.method}</td>
                <td className="px-3 py-3 font-mono text-xs break-all">
                  {entry.path}
                </td>
                <td className="px-3 py-3 font-mono text-xs">
                  {entry.status}
                  {entry.error ? ` ${entry.error}` : ""}
                </td>
              </tr>
            ))}
          </tbody>
        </DataTable>
      ) : null}
    </div>
  )
}
