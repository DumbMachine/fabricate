import type { ReactNode } from "react"
import { cn } from "@/lib/utils"
export type PageTab = { id: string; label: string; count?: number }
export function PageTabs({
  id,
  value,
  onChange,
  items,
  label,
}: {
  id: string
  value: string
  onChange: (value: string) => void
  items: PageTab[]
  label: string
}) {
  return (
    <div
      role="tablist"
      aria-label={label}
      className="relative flex shrink-0 items-end gap-1 overflow-x-auto border-b border-border px-5 pt-1"
      onKeyDown={(e) => {
        if (!["ArrowLeft", "ArrowRight", "Home", "End"].includes(e.key)) return
        const tabs = Array.from(
          e.currentTarget.querySelectorAll<HTMLButtonElement>('[role="tab"]')
        )
        const at = tabs.indexOf(e.target as HTMLButtonElement)
        if (at < 0) return
        e.preventDefault()
        const next =
          e.key === "Home"
            ? 0
            : e.key === "End"
              ? tabs.length - 1
              : (at + (e.key === "ArrowRight" ? 1 : -1) + tabs.length) %
                tabs.length
        tabs[next]?.focus()
        tabs[next]?.click()
      }}
    >
      {items.map((item) => (
        <button
          type="button"
          role="tab"
          id={`${id}-tab-${item.id}`}
          aria-controls={`${id}-panel-${item.id}`}
          aria-selected={value === item.id}
          tabIndex={value === item.id ? 0 : -1}
          onClick={() => onChange(item.id)}
          key={item.id}
          className={cn(
            "relative -mb-px flex h-9 shrink-0 items-center gap-2 rounded-t-lg border-x border-t px-3 text-[13px] outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-inset",
            value === item.id
              ? "z-10 border-border bg-background text-foreground"
              : "border-transparent text-muted-foreground hover:bg-accent hover:text-foreground"
          )}
        >
          {item.label}
          {item.count !== undefined ? (
            <span className="rounded bg-muted px-1.5 font-mono text-[10px] text-muted-foreground">
              {item.count}
            </span>
          ) : null}
        </button>
      ))}
    </div>
  )
}
export function TabPanel({
  id,
  value,
  children,
}: {
  id: string
  value: string
  children: ReactNode
}) {
  return (
    <div
      role="tabpanel"
      id={`${id}-panel-${value}`}
      aria-labelledby={`${id}-tab-${value}`}
      className="flex min-w-0 flex-col gap-5 outline-none"
      tabIndex={0}
    >
      {children}
    </div>
  )
}
