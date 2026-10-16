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

export function errorText(error: unknown): string {
  return error instanceof Error ? error.message : "Request failed"
}

export function matchesQuery(query: string, parts: string[]): boolean {
  const needle = query.trim().toLowerCase()
  if (!needle) return true
  return parts.join("\n").toLowerCase().includes(needle)
}

export function collectionTitle(key: string): string {
  const words = key
    .replace(/[_-]+/g, " ")
    .replace(/([a-z])([A-Z])/g, "$1 $2")
    .split(" ")
    .filter((word) => word.length > 0)
  if (words.length === 0) return key
  return words
    .map((word) => word.slice(0, 1).toUpperCase() + word.slice(1))
    .join(" ")
}

export function collectionSectionId(key: string, index: number): string {
  const slug = key
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
  return `collection-${index}-${slug || "data"}`
}

export function formatTime(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) {
    return value
  }
  return date.toLocaleString()
}
