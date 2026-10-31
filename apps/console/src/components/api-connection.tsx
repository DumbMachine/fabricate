import { useEffect, useState } from "react"
import { CopyButton } from "@/components/copy-button"
import { ErrorNote, LoadingLine } from "@/components/feedback"
import { getResourceContract } from "@/lib/api"
import type { SessionService } from "@/lib/types"

type Contract = Awaited<ReturnType<typeof getResourceContract>>
export function APIConnection({ service }: { service: SessionService }) {
  const [contract, setContract] = useState<Contract | null>(null)
  const [error, setError] = useState("")
  useEffect(() => {
    let cancelled = false
    getResourceContract(service.resource).then(
      (next) => {
        if (!cancelled) setContract(next)
      },
      (failure) => {
        if (!cancelled)
          setError(
            failure instanceof Error
              ? failure.message
              : "Could not load contract"
          )
      }
    )
    return () => {
      cancelled = true
    }
  }, [service.resource])
  return (
    <section className="border-t py-4">
      <h3 className="text-[13px] font-medium">
        {service.displayName} API connection
      </h3>
      {error ? (
        <ErrorNote>{error}</ErrorNote>
      ) : !contract ? (
        <LoadingLine>Loading API contract…</LoadingLine>
      ) : (
        <>
          <div className="mt-3 flex flex-wrap items-center justify-between gap-3">
            <code className="max-w-full min-w-0 text-xs break-all text-muted-foreground">
              {service.url.replace(/\/$/, "") + contract.basePath}
            </code>
            <div className="flex gap-2">
              <CopyButton
                value={service.url.replace(/\/$/, "") + contract.basePath}
                label="Copy API base URL"
              />
              <CopyButton
                value={JSON.stringify(contract.openapi, null, 2)}
                label="Copy OpenAPI"
              />
            </div>
          </div>
          <p className="mt-3 text-xs leading-5 text-muted-foreground">
            The base URL includes the provider’s API path. Paste the OpenAPI
            document into your HTTP API connector and use this service’s token
            from Services.
          </p>
        </>
      )}
    </section>
  )
}
