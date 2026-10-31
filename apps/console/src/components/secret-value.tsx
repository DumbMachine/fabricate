import { useState } from "react"
import { Eye, EyeOff } from "lucide-react"
import { Button } from "@/components/ui/button"
import { CopyButton } from "@/components/copy-button"
export function SecretValue({ value }: { value: string }) {
  const [shown, setShown] = useState(false)
  return (
    <div className="flex flex-col items-start gap-2">
      <code className="max-w-xs text-xs break-all text-muted-foreground">
        {shown ? value : "••••••••••••"}
      </code>
      <div className="flex gap-1">
        <CopyButton value={value} label="Copy token" />
        <Button
          variant="ghost"
          size="icon"
          aria-label={shown ? "Hide token" : "Reveal token"}
          aria-pressed={shown}
          onClick={() => setShown((v) => !v)}
        >
          {shown ? <EyeOff /> : <Eye />}
        </Button>
      </div>
    </div>
  )
}
