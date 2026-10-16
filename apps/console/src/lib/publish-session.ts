import type { Session } from "@/lib/types"

const loopbackNote =
  "The sandbox binds to loopback, so only a process on this machine can reach it."
const sharedHostNote =
  "These service URLs are on this console host. A client that can open the console can call them with the bearer token."

export function publishSession(session: Session): Session {
  if (typeof window === "undefined") {
    return session
  }
  const origin = window.location.origin
  let connectShell = session.connectShell
  let agentPrompt = session.agentPrompt
  const services = session.services.map((service) => {
    if (!isLoopbackURL(service.url)) {
      return service
    }
    const url = `${origin}/s/${session.id}/${service.name}`
    connectShell = connectShell.split(service.url).join(url)
    agentPrompt = agentPrompt.split(service.url).join(url)
    return { ...service, url }
  })
  const variables = session.variables.map((variable) => {
    const service = services.find((item) => item.urlVariable === variable.name)
    if (service === undefined || !isLoopbackURL(variable.value)) {
      return variable
    }
    connectShell = connectShell.split(variable.value).join(service.url)
    agentPrompt = agentPrompt.split(variable.value).join(service.url)
    return { ...variable, value: service.url }
  })
  if (services.some((service) => !isLoopbackURL(service.url))) {
    agentPrompt = agentPrompt.split(loopbackNote).join(sharedHostNote)
  }
  return { ...session, services, variables, connectShell, agentPrompt }
}

export function isLoopbackURL(value: string) {
  try {
    const host = new URL(value).hostname
    return host === "127.0.0.1" || host === "localhost" || host === "::1"
  } catch {
    return false
  }
}
