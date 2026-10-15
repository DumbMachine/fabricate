import { useState } from "react"
import { Button } from "@/components/ui/button"

export function CopyButton({ value, label }: { value: string; label: string }) {
  const [copied, setCopied] = useState(false)
  const [failed, setFailed] = useState(false)

  return (
    <Button
      type="button"
      variant="outline"
      size="sm"
      onClick={() => {
        void navigator.clipboard.writeText(value).then(
          () => {
            setFailed(false)
            setCopied(true)
            window.setTimeout(() => setCopied(false), 1500)
          },
          () => {
            setCopied(false)
            setFailed(true)
          }
        )
      }}
    >
      {copied ? "Copied" : failed ? "Copy failed" : label}
    </Button>
  )
}
