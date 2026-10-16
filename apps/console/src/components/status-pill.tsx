import { Check } from "lucide-react"
import type { ReactNode } from "react"
import { cn } from "cn"

export function StatusPill({
  tone,
  children,
}: {
  tone: "ready" | "held" | "neutral"
  children: ReactNode
}) {
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-xs font-medium",
        tone === "ready" && "bg-success/15 text-success",
        tone === "held" && "bg-sidebar-primary/50 text-foreground",
        tone === "neutral" && "bg-muted text-muted-foreground"
      )}
    >
      {tone === "ready" ? <Check className="size-3" /> : null}
      {children}
    </span>
  )
}
