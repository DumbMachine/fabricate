import { Dialog } from "@base-ui/react/dialog"
import type { ReactNode } from "react"
import { cn } from "@/lib/utils"
export interface DrawerProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  side?: "left" | "right"
  children: ReactNode
  className?: string
  backdropClassName?: string
  ariaLabel?: string
  dismissable?: boolean
}
export function Drawer({
  open,
  onOpenChange,
  side = "right",
  children,
  className,
  backdropClassName,
  ariaLabel = "Details",
  dismissable = true,
}: DrawerProps) {
  return (
    <Dialog.Root
      open={open}
      onOpenChange={onOpenChange}
      disablePointerDismissal={!dismissable}
    >
      <Dialog.Portal>
        <Dialog.Backdrop
          className={cn("fixed inset-0 z-50 bg-black/45", backdropClassName)}
        />
        <Dialog.Popup
          className={cn(
            "fixed inset-y-0 z-50 flex w-80 max-w-[92vw] flex-col border-border bg-background text-foreground shadow-xl outline-none",
            side === "right" ? "right-0 border-l" : "left-0 border-r",
            className
          )}
        >
          <Dialog.Title className="sr-only">{ariaLabel}</Dialog.Title>
          {children}
        </Dialog.Popup>
      </Dialog.Portal>
    </Dialog.Root>
  )
}
