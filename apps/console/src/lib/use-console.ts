import { useNavigate } from "@tanstack/react-router"
import { useEffect, useRef, useState } from "react"
import {
  closeSandbox,
  getCatalog,
  getConfig,
  listSessions,
  startSandbox,
} from "@/lib/api"
import { errorText } from "@/lib/format"
import type { Catalog, ConsoleConfig, StartKind, Session } from "@/lib/types"

export type StartDestination = "sandbox" | "environment" | "stay"

export function useCatalog() {
  const [catalog, setCatalog] = useState<Catalog | null>(null)
  const [error, setError] = useState<string | null>(null)
  useEffect(() => {
    let cancelled = false
    getCatalog()
      .then((next) => {
        if (!cancelled) setCatalog(next)
      })
      .catch((loadError: unknown) => {
        if (!cancelled) setError(errorText(loadError))
      })
    return () => {
      cancelled = true
    }
  }, [])
  return { catalog, error }
}

export function useConsoleConfig() {
  const [config, setConfig] = useState<ConsoleConfig | null>(null)
  useEffect(() => {
    let cancelled = false
    getConfig()
      .then((next) => {
        if (!cancelled) setConfig(next)
      })
      .catch(() => {
        if (!cancelled) setConfig({ proxy: false })
      })
    return () => {
      cancelled = true
    }
  }, [])
  return config
}

export function useSessions() {
  const [sessions, setSessions] = useState<Session[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  useEffect(() => {
    let cancelled = false
    const tick = () => {
      listSessions()
        .then((next) => {
          if (!cancelled) {
            setSessions(next)
            setError(null)
          }
        })
        .catch((error: unknown) => {
          if (!cancelled) setError(errorText(error))
        })
    }
    tick()
    const timer = window.setInterval(tick, 2000)
    return () => {
      cancelled = true
      window.clearInterval(timer)
    }
  }, [])
  return [sessions, setSessions, error] as const
}

export function useSandboxActions() {
  const navigate = useNavigate()
  const pendingRef = useRef(false)
  const [pending, setPending] = useState<string | null>(null)
  const [errors, setErrors] = useState<Record<string, string>>({})

  async function start(
    kind: StartKind,
    id: string,
    destination: StartDestination
  ): Promise<Session | null> {
    if (pendingRef.current) return null
    pendingRef.current = true
    const key = `${kind}:${id}`
    setPending(key)
    setErrors((current) => ({ ...current, [key]: "" }))
    try {
      const session = await startSandbox(kind, id)
      switch (destination) {
        case "sandbox":
          await navigate({ to: "/sandboxes/$id", params: { id: session.id } })
          break
        case "environment":
          await navigate({
            to: "/environments/$name",
            params: { name: id },
          })
          break
        case "stay":
          break
        default: {
          const neverDestination: never = destination
          throw new Error(neverDestination)
        }
      }
      return session
    } catch (startError: unknown) {
      setErrors((current) => ({ ...current, [key]: errorText(startError) }))
      return null
    } finally {
      pendingRef.current = false
      setPending(null)
    }
  }

  async function close(id: string): Promise<boolean> {
    if (pendingRef.current) return false
    pendingRef.current = true
    setPending(`close:${id}`)
    try {
      await closeSandbox(id)
      return true
    } catch (closeError: unknown) {
      setErrors((current) => ({
        ...current,
        [`close:${id}`]: errorText(closeError),
      }))
      return false
    } finally {
      pendingRef.current = false
      setPending(null)
    }
  }

  return { pending, errors, start, close }
}
