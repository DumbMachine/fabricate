import { Link, useRouterState } from "@tanstack/react-router"
import {
  BookOpen,
  Boxes,
  Layers,
  PanelLeft,
  Search,
  Server,
  ChevronRight,
} from "lucide-react"
import { useEffect, useRef, useState, type ReactNode } from "react"
import { cn } from "@/lib/utils"
import { Drawer } from "@/components/ui/drawer"
import { ProxyModeIndicator } from "@/components/proxy-indicator"
import { Button } from "@/components/ui/button"
export type Crumb = { label: string; to?: "/" | "/environments" }
export type FrameSection = { id: string; label: string }
export function ConsoleFrame({
  crumbs,
  heading,
  description,
  actions,
  search,
  sections,
  tabs,
  children,
}: {
  crumbs: Crumb[]
  heading?: string
  eyebrow?: string
  description?: string
  actions?: ReactNode
  search?: { value: string; onChange: (value: string) => void; label?: string }
  sections?: FrameSection[]
  tabs?: ReactNode
  children: ReactNode
}) {
  const pathname = useRouterState({ select: (s) => s.location.pathname })
  const searchRef = useRef<HTMLInputElement>(null)
  const [menuOpen, setMenuOpen] = useState(false)
  useEffect(() => {
    if (!search) return
    const key = (event: KeyboardEvent) => {
      if (
        (event.metaKey || event.ctrlKey) &&
        event.key.toLowerCase() === "k" &&
        !document.querySelector('[role="dialog"]')
      ) {
        event.preventDefault()
        searchRef.current?.focus()
      }
    }
    window.addEventListener("keydown", key)
    return () => window.removeEventListener("keydown", key)
  }, [search])
  useEffect(() => setMenuOpen(false), [pathname])
  return (
    <div className="flex h-svh overflow-hidden bg-background text-foreground">
      <a
        className="sr-only focus:not-sr-only focus:fixed focus:top-3 focus:left-64 focus:z-50"
        href="#main-content"
      >
        Skip to content
      </a>
      <aside className="hidden w-52 shrink-0 flex-col border-r border-sidebar-border bg-sidebar md:flex">
        <Brand />
        <ConsoleNav pathname={pathname} />
      </aside>
      <Drawer
        open={menuOpen}
        onOpenChange={setMenuOpen}
        side="left"
        ariaLabel="Navigation"
        className="bg-sidebar"
      >
        <Brand />
        <ConsoleNav pathname={pathname} />
      </Drawer>
      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex min-h-12 shrink-0 items-center gap-3 px-5">
          <Button
            variant="ghost"
            size="icon"
            aria-label="Open navigation"
            className="md:hidden"
            onClick={() => setMenuOpen(true)}
          >
            <PanelLeft />
          </Button>
          <nav
            aria-label="Breadcrumb"
            className="flex min-w-0 flex-1 items-center gap-2 text-[13px]"
          >
            {crumbs.map((crumb, i) => (
              <span className="flex min-w-0 items-center gap-2" key={i}>
                {i > 0 ? (
                  <ChevronRight className="size-3 shrink-0 text-faint" />
                ) : null}
                {crumb.to ? (
                  <Link
                    to={crumb.to}
                    className="truncate text-muted-foreground hover:text-foreground"
                  >
                    {crumb.label}
                  </Link>
                ) : (
                  <h1 className="truncate font-medium">
                    {heading && i === crumbs.length - 1 ? heading : crumb.label}
                  </h1>
                )}
              </span>
            ))}
          </nav>
          <ProxyModeIndicator />
          {actions ? (
            <div className="flex shrink-0 items-center gap-2">{actions}</div>
          ) : null}
        </header>
        {tabs}
        {sections?.length ? (
          <nav
            aria-label="On this page"
            className="flex gap-4 overflow-x-auto border-b px-5 py-2 text-xs text-muted-foreground"
          >
            {sections.map((section) => (
              <a
                href={`#${section.id}`}
                className="whitespace-nowrap hover:text-foreground"
                key={section.id}
              >
                {section.label}
              </a>
            ))}
          </nav>
        ) : null}
        <main id="main-content" className="min-h-0 flex-1 overflow-y-auto">
          <div className="flex min-w-0 flex-col gap-5 px-5 py-5 sm:px-6">
            {description ? (
              <p className="max-w-3xl text-[13px] leading-6 text-muted-foreground">
                {description}
              </p>
            ) : null}
            {search ? (
              <div className="flex items-center">
                <div className="relative w-full max-w-sm">
                  <Search className="pointer-events-none absolute top-2.5 left-2.5 size-3.5 text-faint" />
                  <input
                    ref={searchRef}
                    type="search"
                    value={search.value}
                    onChange={(e) => search.onChange(e.target.value)}
                    aria-label={search.label || "Search"}
                    placeholder={search.label || "Search"}
                    className="console-input w-full pr-12 pl-8"
                  />
                  <kbd className="pointer-events-none absolute top-2.5 right-2 text-[10px] text-faint">
                    ⌘K
                  </kbd>
                </div>
              </div>
            ) : null}
            {children}
          </div>
        </main>
      </div>
    </div>
  )
}
function Brand() {
  return (
    <Link
      to="/"
      className="flex h-12 shrink-0 items-center gap-2 px-4 text-[13px] font-medium"
    >
      <img
        src="/fabricate-mark.svg"
        className="size-6"
        width={24}
        height={24}
        alt=""
      />
      Fabricate
      <span className="ml-auto rounded border border-sidebar-border px-1.5 py-0.5 text-[10px] font-normal text-faint">
        Console
      </span>
    </Link>
  )
}
function ConsoleNav({ pathname }: { pathname: string }) {
  return (
    <div className="flex min-h-0 flex-1 flex-col gap-1 p-2">
      <p className="px-2 pt-4 pb-2 text-[10px] font-medium tracking-wider text-faint uppercase">
        Workspace
      </p>
      {(
        [
          { to: "/", label: "Sandboxes", icon: Boxes },
          { to: "/environments", label: "Environments", icon: Layers },
          { to: "/deployment", label: "Deployment", icon: Server },
        ] as const
      ).map((item) => {
        const active =
          item.to === "/"
            ? pathname === "/" ||
              pathname.startsWith("/sandboxes/") ||
              pathname.startsWith("/scenarios/")
            : pathname.startsWith(item.to)
        return (
          <Link
            key={item.to}
            to={item.to}
            aria-current={active ? "page" : undefined}
            className={cn(
              "flex h-8 items-center gap-2 rounded-md px-2 text-[13px] transition-colors",
              active
                ? "bg-sidebar-accent text-foreground"
                : "text-muted-foreground hover:bg-sidebar-accent hover:text-foreground"
            )}
          >
            <item.icon className="size-3.5" />
            {item.label}
          </Link>
        )
      })}
      <div className="mt-auto border-t border-sidebar-border pt-2">
        <a
          href="https://fabricate.dmach.in/docs"
          target="_blank"
          rel="noreferrer"
          className="flex h-8 items-center gap-2 rounded-md px-2 text-[13px] text-muted-foreground hover:bg-sidebar-accent"
        >
          <BookOpen className="size-3.5" />
          Documentation
        </a>
        <p className="px-2 py-2 text-[11px] text-faint">
          Isolated service environments
        </p>
      </div>
    </div>
  )
}
export function DataTable({
  children,
  className,
}: {
  children: ReactNode
  className?: string
}) {
  return (
    <div
      role="region"
      aria-label="Data table"
      tabIndex={0}
      className="overflow-x-auto rounded-sm outline-none focus-visible:ring-1 focus-visible:ring-ring"
    >
      <table
        className={cn(
          "w-full min-w-[640px] border-collapse text-[13px] [&_tbody_tr]:transition-colors [&_tbody_tr:hover]:bg-muted/30",
          className
        )}
      >
        {children}
      </table>
    </div>
  )
}
export function ColumnHeading({ children }: { children: ReactNode }) {
  return (
    <th className="border-b px-3 py-2.5 text-left text-[11px] font-normal whitespace-nowrap text-muted-foreground">
      {children}
    </th>
  )
}
