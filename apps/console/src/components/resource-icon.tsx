import { useState } from "react"
import { cn } from "cn"

const pngResources = new Set(["shiprocket"])

type IconSize = "sm" | "md"

export function resourceIconPath(id: string): string {
  const extension = pngResources.has(id) ? "png" : "svg"
  return `/integrations/${id}.${extension}`
}

export function ResourceIcon({
  id,
  size = "sm",
}: {
  id: string
  size?: IconSize
}) {
  const [failed, setFailed] = useState(false)
  const { box, image } = iconMetrics(size)
  return (
    <span
      className={cn(
        "inline-flex shrink-0 items-center justify-center overflow-hidden rounded-md bg-white ring-2 ring-background",
        box
      )}
    >
      {failed ? (
        <span className="text-[10px] font-semibold text-neutral-800 uppercase">
          {id.slice(0, 1) || "?"}
        </span>
      ) : (
        <img
          src={resourceIconPath(id)}
          alt=""
          className={cn("object-contain", image)}
          onError={() => setFailed(true)}
        />
      )}
    </span>
  )
}

function iconMetrics(size: IconSize): { box: string; image: string } {
  switch (size) {
    case "sm":
      return { box: "size-6", image: "size-4" }
    case "md":
      return { box: "size-10", image: "size-7" }
    default: {
      const neverSize: never = size
      return neverSize
    }
  }
}
