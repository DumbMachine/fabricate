import { Link, useRouterState } from "@tanstack/react-router"
import { BookOpen, Boxes, FileJson, Layers, Menu, Search } from "lucide-react"
import { useEffect, useRef, useState, type ReactNode } from "react"
import { cn } from "cn"
import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbLink,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator,
} from "@/components/motion/breadcrumb"
import { BounceSidebar } from "@/components/motion/bounce-sidebar"
import { Drawer } from "@/components/motion/drawer"
import { Input } from "@/components/motion/input"
import { Tabs, TabsList, TabsTrigger } from "@/components/motion/tabs"
import { ProxyModeIndicator } from "@/components/proxy-indicator"
import { Button } from "@/components/ui/button"

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
  const [menuOpen, setMenuOpen] = useState(false)

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

  useEffect(() => {
    setMenuOpen(false)
  }, [pathname])

  return (
    <div className="flex min-h-svh bg-background text-foreground">
      <aside className="sticky top-0 hidden h-svh w-60 shrink-0 flex-col border-r border-sidebar-border bg-sidebar text-sidebar-foreground md:flex">
        <Brand />
        <div className="flex min-h-0 flex-1 flex-col p-3">
          <ConsoleNav pathname={pathname} sections={sections} />
        </div>
      </aside>
      <Drawer
        open={menuOpen}
        onOpenChange={setMenuOpen}
        side="left"
        ariaLabel="Console"
        className="bg-sidebar text-sidebar-foreground"
      >
        <Brand />
        <div className="flex min-h-0 flex-1 flex-col p-3">
          <ConsoleNav
            pathname={pathname}
            sections={sections}
            onNavigate={() => setMenuOpen(false)}
          />
        </div>
      </Drawer>
      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex min-h-14 flex-wrap items-center gap-3 border-b px-4 py-2">
          <Button
            type="button"
            variant="ghost"
            size="icon"
            className="md:hidden"
            aria-label="Open navigation"
            onClick={() => setMenuOpen(true)}
          >
            <Menu className="size-4" />
          </Button>
          <Breadcrumb className="min-w-0 flex-1">
            <BreadcrumbList>
              {crumbs.map((crumb, index) => (
                <BreadcrumbItem key={crumb.label}>
                  {index > 0 ? <BreadcrumbSeparator /> : null}
                  {crumb.to ? (
                    <BreadcrumbLink
                      render={(props) =>
                        crumb.to === "/environments" ? (
                          <Link to="/environments" {...props} />
                        ) : (
                          <Link to="/" {...props} />
                        )
                      }
                    >
                      {crumb.label}
                    </BreadcrumbLink>
                  ) : (
                    <BreadcrumbPage className="truncate">
                      {crumb.label}
                    </BreadcrumbPage>
                  )}
                </BreadcrumbItem>
              ))}
            </BreadcrumbList>
          </Breadcrumb>
          {search ? (
            <Input
              ref={searchRef}
              value={search.value}
              onChange={search.onChange}
              placeholder="Search"
              aria-label={search.label ?? "Search"}
              leftIcon={<Search />}
              rightIcon={<kbd className="mr-4 font-sans text-[11px]">⌘K</kbd>}
              className="w-full sm:ml-auto sm:w-80"
              classNames={{ field: "bg-card", input: "pr-16" }}
            />
          ) : null}
        </header>
        <ProxyModeIndicator />
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
    <Tabs value={value} onValueChange={onChange} variant="segment">
      <TabsList>
        {options.map((option) => (
          <TabsTrigger key={option.id} value={option.id}>
            {option.label}
          </TabsTrigger>
        ))}
      </TabsList>
    </Tabs>
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

export function ColumnHeading({ children }: { children: ReactNode }) {
  return (
    <th className="px-3 py-2 text-left text-[11px] font-medium tracking-wider text-faint uppercase">
      {children}
    </th>
  )
}

function Brand() {
  return (
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
  )
}

function ConsoleNav({
  pathname,
  sections,
  onNavigate,
}: {
  pathname: string
  sections?: FrameSection[]
  onNavigate?: () => void
}) {
  return (
    <div className="flex min-h-0 flex-1 flex-col gap-4">
      <BounceSidebar
        ariaLabel="Console"
        className="w-full"
        listClassName="w-full"
        value={sidebarValue(pathname)}
        onValueChange={() => onNavigate?.()}
        items={[
          {
            id: "sandboxes",
            label: "Sandboxes",
            href: "/",
            icon: <Boxes className="size-4" />,
          },
          {
            id: "environments",
            label: "Environments",
            href: "/environments",
            icon: <Layers className="size-4" />,
          },
        ]}
      />
      {sections && sections.length > 0 ? (
        <div className="border-t border-sidebar-border pt-3">
          <p className="px-2 pb-2 pl-8 text-[11px] font-medium tracking-wider text-faint uppercase">
            On this page
          </p>
          <BounceSidebar
            key={sections.map((section) => section.id).join("|")}
            ariaLabel="On this page"
            listClassName="w-full"
            items={sections.map((section) => ({
              id: section.id,
              label: section.label,
              href: `#${section.id}`,
            }))}
            onValueChange={() => onNavigate?.()}
          />
        </div>
      ) : null}
      <div className="mt-auto flex flex-col">
        <a
          href="/.well-known/fabricate"
          className="flex min-h-9 items-center gap-2 rounded-lg pr-2 pl-8 text-sm font-medium text-muted-foreground hover:text-foreground"
        >
          <FileJson className="size-4" />
          Agent index
        </a>
        <a
          href={docsUrl}
          className="flex min-h-9 items-center gap-2 rounded-lg pr-2 pl-8 text-sm font-medium text-muted-foreground hover:text-foreground"
        >
          <BookOpen className="size-4" />
          Docs
        </a>
      </div>
    </div>
  )
}

function sidebarValue(pathname: string): "sandboxes" | "environments" {
  if (pathname === "/environments" || pathname.startsWith("/environments/")) {
    return "environments"
  }
  return "sandboxes"
}
