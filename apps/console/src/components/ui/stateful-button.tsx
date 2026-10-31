import { forwardRef, type ReactNode } from "react"
import { Check, Loader2, X } from "lucide-react"
import { Button, type ButtonProps } from "./button-base"
export type ButtonState = "idle" | "loading" | "success" | "error"
export type StatefulButtonProps = Omit<ButtonProps, "children"> & {
  state?: ButtonState
  children: ReactNode
  loadingText?: ReactNode
  successText?: ReactNode
  errorText?: ReactNode
  icon?: ReactNode
}
export const StatefulButton = forwardRef<
  HTMLButtonElement,
  StatefulButtonProps
>(function StatefulButton(
  {
    state = "idle",
    children,
    loadingText = "Loading…",
    successText = "Done",
    errorText = "Try again",
    icon,
    disabled,
    ...props
  },
  ref
) {
  const busy = state === "loading"
  const glyph = busy ? (
    <Loader2 className="animate-spin" />
  ) : state === "success" ? (
    <Check />
  ) : state === "error" ? (
    <X />
  ) : (
    icon
  )
  return (
    <Button {...props} ref={ref} disabled={disabled || busy} aria-busy={busy}>
      {glyph ? <span aria-hidden>{glyph}</span> : null}
      <span aria-live="polite" className="inline-flex items-center gap-1.5">
        {busy
          ? loadingText
          : state === "success"
            ? successText
            : state === "error"
              ? errorText
              : children}
      </span>
    </Button>
  )
})
