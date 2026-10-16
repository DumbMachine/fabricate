import { useState } from "react"
import { StatefulButton } from "@/components/ui/button"

export function CopyButton({ value, label }: { value: string; label: string }) {
  const [copied, setCopied] = useState(false)
  const [failed, setFailed] = useState(false)

  return (
    <StatefulButton
      type="button"
      variant="outline"
      size="sm"
      state={copied ? "success" : failed ? "error" : "idle"}
      successText="Copied"
      errorText="Copy failed"
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
      {label}
    </StatefulButton>
  )
}
