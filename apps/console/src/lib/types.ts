export type SandboxKind = "environment" | "scenario"

export type ConsoleConfig = {
  proxy: boolean
  publicUrl?: string
}

export type CatalogService = {
  name: string
  resource: string
  displayName: string
  scenario: string
  providerHosts: string[]
}

export type CatalogEnvironment = {
  name: string
  label: string
  services: CatalogService[]
}

export type CatalogScenario = {
  id: string
  resource: string
  displayName: string
  resourceVersion: string
  providerHosts: string[]
}

export type Catalog = {
  environments: CatalogEnvironment[]
  scenarios: CatalogScenario[]
}

export type ScenarioScalar = {
  key: string
  value: string
}

export type ScenarioCollection = {
  key: string
  total: number
  shown: number
  columns: string[]
  rows: Record<string, string>[]
}

export type ScenarioView = {
  id: string
  resource: string
  displayName: string
  resourceVersion: string
  providerHosts: string[]
  scalars: ScenarioScalar[]
  collections: ScenarioCollection[]
}

export type Variable = {
  name: string
  value: string
}

export type SessionService = {
  name: string
  resource: string
  displayName: string
  scenario: string
  url: string
  token: string
  urlVariable: string
  tokenVariable: string
  providerHosts: string[]
}

export type ProxyConnection = {
  url: string
  caPath: string
  hosts: string[]
}

export type Session = {
  id: string
  name: string
  label: string
  kind: SandboxKind
  status: string
  proxy: boolean
  startedAt: string
  requestLog: string
  services: SessionService[]
  variables: Variable[]
  proxyConnection?: ProxyConnection
  connectShell: string
  agentPrompt: string
  anchored: boolean
}
