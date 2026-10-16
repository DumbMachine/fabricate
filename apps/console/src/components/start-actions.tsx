import { Button } from "@/components/ui/button"

export function StartActions({
  pending,
  directKey,
  proxyKey,
  onStart,
  startLabel = "Start",
  proxyLabel = "Proxy",
}: {
  pending: string | null
  directKey: string
  proxyKey: string
  onStart: (proxy: boolean) => void
  startLabel?: string
  proxyLabel?: string
}) {
  const locked = pending !== null
  return (
    <div className="flex flex-wrap justify-end gap-2">
      <Button
        type="button"
        size="sm"
        disabled={locked}
        onClick={() => onStart(false)}
      >
        {pending === directKey ? "Starting…" : startLabel}
      </Button>
      <Button
        type="button"
        variant="outline"
        size="sm"
        disabled={locked}
        onClick={() => onStart(true)}
      >
        {pending === proxyKey ? "Starting…" : proxyLabel}
      </Button>
    </div>
  )
}
