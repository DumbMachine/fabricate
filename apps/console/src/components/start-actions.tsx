import { StatefulButton } from "@/components/ui/button"

export function StartActions({
  pending,
  actionKey,
  onStart,
  label = "Start",
}: {
  pending: string | null
  actionKey: string
  onStart: () => void
  label?: string
}) {
  const locked = pending !== null
  return (
    <div className="flex flex-wrap justify-end gap-2">
      <StatefulButton
        type="button"
        size="sm"
        state={pending === actionKey ? "loading" : "idle"}
        loadingText="Starting…"
        disabled={locked && pending !== actionKey}
        onClick={onStart}
      >
        {label}
      </StatefulButton>
    </div>
  )
}
