import { StatefulButton } from "@/components/ui/button"

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
      <StatefulButton
        type="button"
        size="sm"
        state={pending === directKey ? "loading" : "idle"}
        loadingText="Starting…"
        disabled={locked && pending !== directKey}
        onClick={() => onStart(false)}
      >
        {startLabel}
      </StatefulButton>
      <StatefulButton
        type="button"
        variant="outline"
        size="sm"
        state={pending === proxyKey ? "loading" : "idle"}
        loadingText="Starting…"
        disabled={locked && pending !== proxyKey}
        onClick={() => onStart(true)}
      >
        {proxyLabel}
      </StatefulButton>
    </div>
  )
}
