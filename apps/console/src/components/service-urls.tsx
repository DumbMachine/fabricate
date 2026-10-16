import type { SessionService } from "@/lib/types"

export function ServiceURLs({ services }: { services: SessionService[] }) {
  if (services.length === 0) {
    return <span className="text-sm text-muted-foreground">No URL</span>
  }
  return (
    <ul className="flex max-w-md flex-col gap-2">
      {services.map((service) => (
        <li key={service.name} className="min-w-0">
          {services.length > 1 ? (
            <p className="text-[11px] tracking-wide text-faint uppercase">
              {service.name}
            </p>
          ) : null}
          <p className="font-mono text-xs break-all">{service.url}</p>
        </li>
      ))}
    </ul>
  )
}
