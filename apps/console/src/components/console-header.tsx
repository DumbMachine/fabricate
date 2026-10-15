import { Link } from "@tanstack/react-router"
import type { ReactNode } from "react"

export function ConsoleHeader({ children }: { children?: ReactNode }) {
  return (
    <header className="border-b">
      <div className="mx-auto flex max-w-6xl items-center justify-between gap-4 px-6 py-4">
        <Link to="/" className="min-w-0">
          <p className="text-sm text-muted-foreground">Fabricate</p>
          <p className="text-lg font-medium tracking-tight">Sandboxes</p>
        </Link>
        {children}
      </div>
    </header>
  )
}
