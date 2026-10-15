import type { SandboxKind } from "@/lib/types"

export const agents = ["Cursor", "Claude Code", "Codex", "Devin"] as const

export type Agent = (typeof agents)[number]

export function kindLabel(kind: SandboxKind): string {
  switch (kind) {
    case "environment":
      return "Environment"
    case "scenario":
      return "Scenario"
    default: {
      const neverKind: never = kind
      return neverKind
    }
  }
}

export function withAgent(agent: Agent, prompt: string): string {
  return `You are working in ${agent}.\n\n${prompt}`
}

export function formatTime(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) {
    return value
  }
  return date.toLocaleString()
}
