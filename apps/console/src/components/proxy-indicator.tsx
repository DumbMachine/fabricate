import { useConsoleConfig } from "@/lib/use-console"
export function ProxyModeIndicator() {
  const config = useConsoleConfig()
  return config?.proxy ? (
    <span
      className="rounded border border-border px-1.5 py-0.5 text-[10px] text-muted-foreground"
      title="Clients use production hostnames through this deployment’s proxy"
    >
      Proxy mode
    </span>
  ) : null
}
