import type { ReactNode } from "react"
export function StatusPill({
  tone,
  children,
}: {
  tone: "ready" | "held" | "neutral"
  children: ReactNode
}) {
  return (
    <span className="inline-flex w-fit items-center gap-1.5 text-xs whitespace-nowrap text-muted-foreground">
      <span
        aria-hidden
        className={`size-1.5 rounded-full ${tone === "ready" ? "bg-success" : tone === "held" ? "bg-amber-400" : "bg-faint"}`}
      />
      {children}
    </span>
  )
}
