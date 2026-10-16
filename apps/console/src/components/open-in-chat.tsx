import { Menu } from "@base-ui/react/menu"
import { ChevronDown, ExternalLink } from "lucide-react"
import { buttonClassName } from "@/components/ui/button"

type DestinationId = "cursor" | "claude" | "chatgpt" | "email"

const chatDestinations: DestinationId[] = ["cursor", "claude", "chatgpt"]

export function OpenInChat({
  query,
  subject,
}: {
  query: string
  subject: string
}) {
  return (
    <Menu.Root modal={false}>
      <Menu.Trigger className={buttonClassName("outline", "sm")}>
        Open in chat
        <ChevronDown className="size-3.5" aria-hidden />
      </Menu.Trigger>
      <Menu.Portal>
        <Menu.Positioner
          className="z-50"
          side="bottom"
          align="end"
          sideOffset={6}
        >
          <Menu.Popup className="min-w-56 border border-border bg-card p-1 text-sm text-foreground outline-none">
            <Menu.Group>
              <Menu.GroupLabel className="px-2 py-1.5 text-[11px] font-medium tracking-wider text-faint uppercase">
                Chat
              </Menu.GroupLabel>
              {chatDestinations.map((id) => (
                <DestinationItem
                  key={id}
                  id={id}
                  href={destinationHref(id, query, subject)}
                />
              ))}
            </Menu.Group>
            <Menu.Separator className="my-1 h-px bg-border" />
            <Menu.Group>
              <Menu.GroupLabel className="px-2 py-1.5 text-[11px] font-medium tracking-wider text-faint uppercase">
                Email
              </Menu.GroupLabel>
              <DestinationItem
                id="email"
                href={destinationHref("email", query, subject)}
              />
            </Menu.Group>
          </Menu.Popup>
        </Menu.Positioner>
      </Menu.Portal>
    </Menu.Root>
  )
}

function DestinationItem({ id, href }: { id: DestinationId; href: string }) {
  const external = id !== "email"
  return (
    <Menu.LinkItem
      href={href}
      closeOnClick
      target={external ? "_blank" : undefined}
      rel={external ? "noreferrer" : undefined}
      className="flex items-center gap-2 rounded-md px-2 py-1.5 outline-none data-highlighted:bg-muted"
    >
      <span className="min-w-0 flex-1">{destinationLabel(id)}</span>
      <ExternalLink className="size-3.5 text-muted-foreground" aria-hidden />
    </Menu.LinkItem>
  )
}

function destinationLabel(id: DestinationId): string {
  switch (id) {
    case "cursor":
      return "Open in Cursor"
    case "claude":
      return "Open in Claude"
    case "chatgpt":
      return "Open in ChatGPT"
    case "email":
      return "Email"
    default: {
      const neverId: never = id
      return neverId
    }
  }
}

function destinationHref(
  id: DestinationId,
  query: string,
  subject: string
): string {
  switch (id) {
    case "cursor": {
      const url = new URL("https://cursor.com/link/prompt")
      url.searchParams.set("text", query)
      return url.toString()
    }
    case "claude": {
      const url = new URL("https://claude.ai/new")
      url.searchParams.set("q", query)
      return url.toString()
    }
    case "chatgpt": {
      const url = new URL("https://chatgpt.com/")
      url.searchParams.set("hints", "search")
      url.searchParams.set("prompt", query)
      return url.toString()
    }
    case "email":
      return `mailto:?subject=${encodeURIComponent(subject)}&body=${encodeURIComponent(query)}`
    default: {
      const neverId: never = id
      return neverId
    }
  }
}
