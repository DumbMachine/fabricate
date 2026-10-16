import { useState } from "react"
import { AnimatedBadge } from "@/components/motion/animated-badge"
import { useConsoleConfig } from "@/lib/use-console"

const explanation =
  "Proxy mode keeps each provider's production hostname. A client honors HTTP_PROXY, HTTPS_PROXY, and the CA file from the sandbox. The proxy replaces Authorization on those hosts, so the client does not send the service token there. This console was started in proxy mode, and every sandbox starts that way."

export function ProxyModeIndicator() {
  const config = useConsoleConfig()
  const [pinned, setPinned] = useState(false)
  if (!config?.proxy) return null
  return (
    <div className="relative z-20 border-b px-4 py-2 sm:px-6">
      <span
        className="group relative inline-flex"
        data-open={pinned ? "true" : "false"}
      >
        <button
          type="button"
          className="rounded-md focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary"
          aria-describedby="proxy-mode-explanation"
          onClick={() => setPinned((open) => !open)}
        >
          <AnimatedBadge status="info" size="sm" showIcon>
            Running in proxy mode
          </AnimatedBadge>
        </button>
        <span
          id="proxy-mode-explanation"
          role="tooltip"
          className="pointer-events-none absolute top-full left-0 z-20 mt-2 w-80 max-w-[calc(100vw-2rem)] border bg-card p-3 text-sm leading-6 text-muted-foreground opacity-0 group-focus-within:opacity-100 group-hover:opacity-100 group-data-[open=true]:opacity-100"
        >
          {explanation}
        </span>
      </span>
    </div>
  )
}
