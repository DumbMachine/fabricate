import { Dialog } from "@base-ui/react/dialog"
import { useState } from "react"
import { Button, StatefulButton } from "@/components/ui/button"
export function CloseSandboxButton({
  onClose,
  pending = false,
  disabled = false,
  label = "Close sandbox",
}: {
  onClose: () => void
  pending?: boolean
  disabled?: boolean
  label?: string
}) {
  const [open, setOpen] = useState(false)
  return (
    <Dialog.Root open={open} onOpenChange={setOpen}>
      <Dialog.Trigger
        render={
          <StatefulButton
            variant="ghost"
            size="sm"
            state={pending ? "loading" : "idle"}
            loadingText="Closing…"
            disabled={disabled}
            children={label}
          />
        }
      >
        {label}
      </Dialog.Trigger>
      <Dialog.Portal>
        <Dialog.Backdrop className="fixed inset-0 z-50 bg-black/50" />
        <Dialog.Popup className="fixed top-1/2 left-1/2 z-50 w-[calc(100%-2rem)] max-w-sm -translate-x-1/2 -translate-y-1/2 rounded-xl border bg-popover p-5 text-foreground shadow-xl outline-none">
          <Dialog.Title className="text-base font-medium">
            Close this sandbox?
          </Dialog.Title>
          <Dialog.Description className="mt-2 text-[13px] leading-6 text-muted-foreground">
            Its services will stop and sandbox data will be discarded. Save any
            results you need before closing.
          </Dialog.Description>
          <div className="mt-6 flex justify-end gap-2">
            <Dialog.Close render={<Button variant="outline" size="sm" />}>
              Keep running
            </Dialog.Close>
            <Button
              variant="destructive"
              size="sm"
              onClick={() => {
                setOpen(false)
                onClose()
              }}
            >
              Close sandbox
            </Button>
          </div>
        </Dialog.Popup>
      </Dialog.Portal>
    </Dialog.Root>
  )
}
