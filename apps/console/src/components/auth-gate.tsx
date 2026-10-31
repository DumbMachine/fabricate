import {
  createContext,
  useContext,
  useEffect,
  useState,
  type ReactNode,
  type FormEvent,
} from "react"
import { LockKeyhole, ArrowRight } from "lucide-react"
import { getAuthSession, signIn, signOut } from "@/lib/api"
import { Button, StatefulButton } from "@/components/ui/button"
import type { AuthSession } from "@/lib/types"
const AuthContext = createContext<{
  required: boolean
  logout: () => Promise<void>
} | null>(null)
export const useConsoleAuth = () => useContext(AuthContext)
export function AuthGate({ children }: { children: ReactNode }) {
  const [session, setSession] = useState<AuthSession | null>(null),
    [error, setError] = useState(""),
    [token, setToken] = useState(""),
    [busy, setBusy] = useState(false)
  const refresh = () => {
    setError("")
    getAuthSession()
      .then(setSession)
      .catch(() =>
        setError("Cannot reach Fabricate. Check the connection and try again.")
      )
  }
  useEffect(() => {
    refresh()
    const expired = () => {
      setSession({ required: true, authenticated: false })
      setToken("")
      setError("Your session ended. Sign in again to continue.")
    }
    window.addEventListener("fabricate:unauthorized", expired)
    return () => window.removeEventListener("fabricate:unauthorized", expired)
  }, [])
  async function submit(event: FormEvent) {
    event.preventDefault()
    if (busy) return
    setBusy(true)
    setError("")
    try {
      setSession(await signIn(token))
      setToken("")
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not sign in")
    } finally {
      setBusy(false)
    }
  }
  if (session?.authenticated)
    return (
      <AuthContext.Provider
        value={{
          required: session.required,
          logout: async () => {
            await signOut()
            setSession({ required: true, authenticated: false })
          },
        }}
      >
        {children}
      </AuthContext.Provider>
    )
  return (
    <main className="grid min-h-svh place-items-center bg-background px-6">
      <div className="w-full max-w-sm">
        <div className="mb-12 flex items-center gap-2.5 text-sm font-medium">
          <img src="/fabricate-mark.svg" alt="" className="size-7" />
          Fabricate
        </div>
        {!session ? (
          <>
            <h1 className="text-lg font-medium">
              {error ? "Connection unavailable" : "Connecting to Fabricate"}
            </h1>
            <p role="status" className="mt-3 text-sm text-muted-foreground">
              {error || "Checking this deployment…"}
            </p>
            {error ? (
              <Button className="mt-5" onClick={refresh}>
                Try again
              </Button>
            ) : null}
          </>
        ) : (
          <form onSubmit={(e) => void submit(e)}>
            <LockKeyhole className="mb-4 size-5 text-muted-foreground" />
            <h1 className="text-xl font-medium tracking-tight">
              Sign in to Fabricate
            </h1>
            <p className="mt-2 mb-7 text-sm leading-6 text-muted-foreground">
              Use your deployment’s operator token to manage sandboxes and
              connections.
            </p>
            <label className="mb-2 block text-[13px]" htmlFor="operator-token">
              Operator token
            </label>
            <input
              id="operator-token"
              type="password"
              autoComplete="off"
              autoFocus
              value={token}
              onChange={(e) => setToken(e.target.value)}
              disabled={busy}
              required
              className="console-input w-full"
            />
            {error ? (
              <p role="alert" className="mt-3 text-sm text-destructive">
                {error}
              </p>
            ) : null}
            <StatefulButton
              type="submit"
              state={busy ? "loading" : "idle"}
              loadingText="Signing in…"
              disabled={!token}
              className="mt-5 w-full"
            >
              Continue <ArrowRight />
            </StatefulButton>
            <p className="mt-4 text-xs leading-5 text-muted-foreground">
              The token is exchanged for an eight-hour browser session. It isn’t
              saved in browser storage.
            </p>
          </form>
        )}
      </div>
    </main>
  )
}
