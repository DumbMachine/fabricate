import type { ReactNode } from "react"
import { AnimatedBadge } from "@/components/motion/animated-badge"

export function StatusPill({
  tone,
  children,
}: {
  tone: "ready" | "held" | "neutral"
  children: ReactNode
}) {
  return (
    <AnimatedBadge
      status={badgeStatus(tone)}
      size="sm"
      pulse={tone === "ready"}
      className="w-fit self-start"
    >
      {children}
    </AnimatedBadge>
  )
}

function badgeStatus(tone: "ready" | "held" | "neutral") {
  switch (tone) {
    case "ready":
      return "success" as const
    case "held":
      return "neutral" as const
    case "neutral":
      return "neutral" as const
    default: {
      const neverTone: never = tone
      return neverTone
    }
  }
}
