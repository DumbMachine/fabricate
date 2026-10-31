import { Loader2, AlertCircle } from "lucide-react"
export function LoadingLine({ children }: { children: string }) {
  return (
    <p
      role="status"
      className="flex items-center gap-2 text-[13px] text-muted-foreground"
    >
      <Loader2 aria-hidden className="size-3.5 animate-spin" />
      {children}
    </p>
  )
}
export function ErrorNote({
  children,
  compact = false,
}: {
  children: string
  compact?: boolean
}) {
  return (
    <div
      role="alert"
      className={`flex items-start gap-2 rounded-lg bg-destructive/10 text-[13px] leading-5 text-destructive ${compact ? "p-2" : "px-3 py-2.5"}`}
    >
      <AlertCircle aria-hidden className="mt-0.5 size-3.5 shrink-0" />
      <p>{children}</p>
    </div>
  )
}
