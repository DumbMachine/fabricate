import { Link, useRouterState } from "@tanstack/react-router"
import { BookOpen, Boxes, Layers, Search } from "lucide-react"
import { useEffect, useRef, type ReactNode } from "react"
import { cn } from "cn"

const docsUrl = "https://fabricate.dmach.in/docs"

export type Crumb = {
  label: string
  to?: "/" | "/environments"
}

export type FrameSection = {
  id: string
  label: string
}

export function ConsoleFrame({
  crumbs,
  heading,
  eyebrow,
  description,
  actions,
  search,
  sections,
  children,
}: {
  crumbs: Crumb[]
  heading?: string
  eyebrow?: string
  description?: string
  actions?: ReactNode
  search?: {
    value: string
    onChange: (value: string) => void
    label?: string
  }
  sections?: FrameSection[]
  children: ReactNode
}) {
  const pathname = useRouterState({
    select: (state) => state.location.pathname,
  })
  const searchRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    if (!search) return
    const onKey = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") {
        event.preventDefault()
        searchRef.current?.focus()
      }
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [search])

  return (
    <div className="flex min-h-svh bg-background text-foreground">
      <aside className="sticky top-0 hidden h-svh w-60 shrink-0 flex-col border-r border-sidebar-border bg-sidebar text-sidebar-foreground md:flex">
        <div className="flex h-14 items-center gap-2 border-b border-sidebar-border px-3">
          <img
            src="/fabricate-mark.svg"
            width={28}
            height={28}
            alt=""
            className="size-7"
          />
          <span className="font-brand text-[1.45rem] leading-[0.8] font-semibold tracking-[0.025em] uppercase">
            Fabricate
          </span>
        </div>
        <nav className="flex flex-1 flex-col gap-1 p-2">
          <SideLink to="/" active={navActive(pathname, "sandboxes")}>
            <Boxes className="size-4" />
            Sandboxes
          </SideLink>
          <SideLink
            to="/environments"
            active={navActive(pathname, "environments")}
          >
            <Layers className="size-4" />
            Environments
          </SideLink>
          {sections ? (
            <div className="mt-3 flex flex-col gap-1 border-t border-sidebar-border pt-3">
              <p className="px-2 pb-1 text-[11px] font-medium tracking-wider text-faint uppercase">
                On this page
              </p>
              {sections.map((section) => (
                <a
                  key={section.id}
                  href={`#${section.id}`}
                  className="rounded-lg px-2 py-1.5 text-sm text-sidebar-foreground hover:bg-muted hover:text-foreground"
                >
                  {section.label}
                </a>
              ))}
            </div>
          ) : null}
        </nav>
        <div className="border-t border-sidebar-border p-2">
          <a
            href={docsUrl}
            className="flex items-center gap-2 rounded-lg px-2 py-1.5 text-sm text-muted-foreground hover:bg-muted hover:text-foreground"
          >
            <BookOpen className="size-4" />
            Docs
          </a>
        </div>
      </aside>
      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex h-14 items-center gap-3 border-b px-4">
          <nav className="flex min-w-0 items-center gap-2 text-sm">
            {crumbs.map((crumb, index) => (
              <span
                key={crumb.label}
                className="flex min-w-0 items-center gap-2"
              >
                {index > 0 ? (
                  <span className="text-muted-foreground">/</span>
                ) : null}
                {crumb.to ? (
                  <Link
                    to={crumb.to}
                    className="truncate text-muted-foreground hover:text-foreground"
                  >
                    {crumb.label}
                  </Link>
                ) : (
                  <span className="truncate">{crumb.label}</span>
                )}
              </span>
            ))}
          </nav>
          {search ? (
            <label className="ml-auto flex h-8 w-full max-w-xs items-center gap-2 rounded-md border bg-card px-2 text-sm text-muted-foreground">
              <Search className="size-3.5" />
              <input
                ref={searchRef}
                value={search.value}
                onChange={(event) => search.onChange(event.target.value)}
                placeholder={search.label ?? "Search"}
                aria-label={search.label ?? "Search"}
                className="w-full bg-transparent text-foreground outline-none placeholder:text-muted-foreground"
              />
              <kbd className="hidden font-sans text-[11px] sm:inline">⌘K</kbd>
            </label>
          ) : null}
        </header>
        <div className="flex gap-2 overflow-x-auto border-b px-4 py-2 md:hidden">
          <Link
            to="/"
            className={cn(
              "text-sm whitespace-nowrap",
              navActive(pathname, "sandboxes")
                ? "text-foreground"
                : "text-muted-foreground"
            )}
          >
            Sandboxes
          </Link>
          <Link
            to="/environments"
            className={cn(
              "text-sm whitespace-nowrap",
              navActive(pathname, "environments")
                ? "text-foreground"
                : "text-muted-foreground"
            )}
          >
            Environments
          </Link>
          {sections?.map((section) => (
            <a
              key={section.id}
              href={`#${section.id}`}
              className="text-sm whitespace-nowrap"
            >
              {section.label}
            </a>
          ))}
        </div>
        <main className="flex flex-1 flex-col gap-8 px-4 py-6 sm:px-6">
          {heading ? (
            <div className="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
              <div className="flex flex-col gap-2">
                {eyebrow ? (
                  <p className="text-[11px] font-medium tracking-[0.14em] text-faint uppercase">
                    {eyebrow}
                  </p>
                ) : null}
                <h1 className="text-3xl font-bold tracking-[-0.035em]">
                  {heading}
                </h1>
                {description ? (
                  <p className="max-w-2xl text-sm leading-6 text-muted-foreground">
                    {description}
                  </p>
                ) : null}
              </div>
              {actions}
            </div>
          ) : null}
          {children}
        </main>
      </div>
    </div>
  )
}

export function FilterTabs({
  options,
  value,
  onChange,
}: {
  options: { id: string; label: string }[]
  value: string
  onChange: (id: string) => void
}) {
  return (
    <div className="inline-flex border">
      {options.map((option, index) => (
        <button
          key={option.id}
          type="button"
          onClick={() => onChange(option.id)}
          className={cn(
            "px-3 py-1 text-sm",
            index > 0 && "border-l",
            value === option.id
              ? "bg-sidebar-primary text-sidebar-primary-foreground"
              : "text-muted-foreground hover:bg-muted hover:text-foreground"
          )}
        >
          {option.label}
        </button>
      ))}
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
    <div className="overflow-x-auto border-y">
      <table
        className={cn(
          "w-full min-w-[720px] border-collapse text-sm",
          className
        )}
      >
        {children}
      </table>
    </div>
  )
}

function SideLink({
  to,
  active,
  children,
}: {
  to: "/" | "/environments"
  active: boolean
  children: ReactNode
}) {
  return (
    <Link
      to={to}
      className={cn(
        "flex items-center gap-2 rounded-lg px-2 py-1.5 text-sm",
        active
          ? "bg-sidebar-primary text-sidebar-primary-foreground"
          : "text-sidebar-foreground hover:bg-muted hover:text-foreground"
      )}
    >
      {children}
    </Link>
  )
}

function navActive(
  pathname: string,
  item: "sandboxes" | "environments"
): boolean {
  switch (item) {
    case "sandboxes":
      return (
        pathname === "/" ||
        pathname.startsWith("/sandboxes") ||
        pathname.startsWith("/scenarios")
      )
    case "environments":
      return (
        pathname === "/environments" || pathname.startsWith("/environments/")
      )
    default: {
      const neverItem: never = item
      return neverItem
    }
  }
}

export function ColumnHeading({ children }: { children: ReactNode }) {
  return (
    <th className="px-3 py-2 text-left text-[11px] font-medium tracking-wider text-faint uppercase">
      {children}
    </th>
  )
}
