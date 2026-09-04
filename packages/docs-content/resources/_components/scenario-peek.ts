export const OFFICIAL_SCENARIO_REPO = "DumbMachine/fabricate";
export const OFFICIAL_SCENARIO_REF = "main";
export const DEFAULT_ROW_LIMIT = 25;
export const DEFAULT_COLUMN_LIMIT = 10;
export const DEFAULT_CELL_CHARS = 96;
export const PEEK_FETCH_TIMEOUT_MS = 12_000;
export const PEEK_SLOW_MS = 1_600;

const RESOURCE_ID = /^[a-z][a-z0-9-]*$/;
const SCENARIO_FILE = /^[A-Za-z0-9._-]+\.json$/;
const IDENTITY_COLUMNS = ["gid", "id", "name", "title", "subject", "email", "status", "type"];

export type ScenarioScalar = {
  key: string;
  value: string;
};

export type ScenarioCollection = {
  key: string;
  rows: Record<string, unknown>[];
};

export type ScenarioPeekData = {
  id: string;
  resource: string;
  scalars: ScenarioScalar[];
  collections: ScenarioCollection[];
};

export type ResolvedScenarioSource = {
  blobURL: string;
  path: string;
  rawURL: string;
};

export type PeekLoadKind =
  | "http"
  | "invalid"
  | "missing-source"
  | "network"
  | "not-found"
  | "timeout";

export type PeekLoadFailure = {
  kind: PeekLoadKind;
  title: string;
  message: string;
  retryable: boolean;
};

export type PeekLoadResult =
  | {ok: true; data: ScenarioPeekData}
  | {ok: false; error: PeekLoadFailure};

function peekFailure(kind: PeekLoadKind, title: string, message: string, retryable: boolean): PeekLoadFailure {
  return {kind, title, message, retryable};
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function truncate(value: string, maxChars: number): string {
  if (value.length <= maxChars) {
    return value;
  }
  return `${value.slice(0, Math.max(0, maxChars - 1)).trimEnd()}…`;
}

export function scenarioFileName(resource: string, scenario: string): string | null {
  if (!RESOURCE_ID.test(resource)) {
    return null;
  }
  const prefix = `${resource}.`;
  let name = scenario.startsWith(prefix) ? scenario.slice(prefix.length) : scenario;
  if (!name.endsWith(".json")) {
    name = `${name}.json`;
  }
  return SCENARIO_FILE.test(name) ? name : null;
}

export function officialScenarioSource(resource: string, scenario: string): ResolvedScenarioSource | null {
  const file = scenarioFileName(resource, scenario);
  if (!file) {
    return null;
  }
  const path = `resources/${resource}/scenarios/${file}`;
  return {
    path,
    blobURL: `https://github.com/${OFFICIAL_SCENARIO_REPO}/blob/${OFFICIAL_SCENARIO_REF}/${path}`,
    rawURL: `https://raw.githubusercontent.com/${OFFICIAL_SCENARIO_REPO}/${OFFICIAL_SCENARIO_REF}/${path}`,
  };
}

export function formatCell(value: unknown, maxChars = DEFAULT_CELL_CHARS): string {
  if (value === null || value === undefined) {
    return "";
  }
  if (typeof value === "string") {
    return truncate(value.replace(/\s+/g, " ").trim(), maxChars);
  }
  if (typeof value === "number" || typeof value === "boolean") {
    return String(value);
  }
  if (Array.isArray(value)) {
    const primitive = value.every(
      (item) => typeof item === "string" || typeof item === "number" || typeof item === "boolean",
    );
    return truncate(primitive ? value.map(String).join(", ") : JSON.stringify(value), maxChars);
  }
  if (typeof value === "object") {
    return truncate(JSON.stringify(value), maxChars);
  }
  return "";
}

export function collectionColumns(
  rows: Record<string, unknown>[],
  columnLimit = DEFAULT_COLUMN_LIMIT,
): string[] {
  const seen: string[] = [];
  for (const row of rows) {
    for (const key of Object.keys(row)) {
      if (!seen.includes(key)) {
        seen.push(key);
      }
    }
  }
  const preferred = IDENTITY_COLUMNS.filter((key) => seen.includes(key));
  const rest = seen.filter((key) => !preferred.includes(key));
  return [...preferred, ...rest].slice(0, Math.max(1, columnLimit));
}

export function parseScenarioPeek(value: unknown): ScenarioPeekData | null {
  if (!isRecord(value) || typeof value.$id !== "string" || typeof value.$resource !== "string" || !isRecord(value.state)) {
    return null;
  }
  const scalars: ScenarioScalar[] = [];
  const collections: ScenarioCollection[] = [];
  for (const [key, entry] of Object.entries(value.state)) {
    if (Array.isArray(entry)) {
      const rows = entry.filter(isRecord);
      collections.push({key, rows});
      continue;
    }
    const formatted = formatCell(entry);
    if (formatted) {
      scalars.push({key, value: formatted});
    }
  }
  if (collections.length === 0) {
    return null;
  }
  return {
    id: value.$id,
    resource: value.$resource,
    scalars,
    collections,
  };
}

export function selectCollection(
  collections: ScenarioCollection[],
  preferred?: string,
): ScenarioCollection | null {
  if (collections.length === 0) {
    return null;
  }
  return collections.find((entry) => entry.key === preferred) ?? collections[0];
}

export function limitedRows(rows: Record<string, unknown>[], limit = DEFAULT_ROW_LIMIT): Record<string, unknown>[] {
  return rows.slice(0, Math.max(0, limit));
}

export function peekTitle(resolved: ResolvedScenarioSource | null, scenario?: string): string {
  if (resolved) {
    const match = resolved.path.match(/^resources\/([^/]+)\/scenarios\/(.+)\.json$/);
    if (match) {
      return `${match[1]}.${match[2]}`;
    }
  }
  if (scenario) {
    return scenario;
  }
  return "Scenario data";
}

export function missingPeekSource(): PeekLoadFailure {
  return peekFailure(
    "missing-source",
    "Peek is not configured",
    "Pass a resource and scenario id so this table knows which committed JSON to load.",
    false,
  );
}

export function classifyPeekHttpStatus(status: number): PeekLoadFailure | null {
  if (status >= 200 && status < 300) {
    return null;
  }
  if (status === 404 || status === 410) {
    return peekFailure(
      "not-found",
      "Scenario file not found",
      "GitHub does not have this scenario. It may have been renamed, or it is not on main yet.",
      true,
    );
  }
  if (status === 403 || status === 429) {
    return peekFailure(
      "http",
      "GitHub blocked the request",
      "This usually means a rate limit. Wait a moment and retry.",
      true,
    );
  }
  if (status >= 500) {
    return peekFailure(
      "http",
      "GitHub had an error",
      "The scenario file could not be served. Retry in a moment.",
      true,
    );
  }
  return peekFailure(
    "http",
    "Scenario could not be loaded",
    `GitHub returned HTTP ${status}. Retry, or open the file from the GitHub icon.`,
    true,
  );
}

export function classifyPeekException(error: unknown, signal?: AbortSignal): PeekLoadFailure | null {
  if (signal?.aborted && signal.reason !== "timeout") {
    return null;
  }
  if (signal?.reason === "timeout" || (error instanceof DOMException && error.name === "TimeoutError")) {
    return peekFailure(
      "timeout",
      "This is taking too long",
      "GitHub did not respond in time. Retry to load the scenario again.",
      true,
    );
  }
  if (error instanceof SyntaxError) {
    return peekFailure(
      "invalid",
      "Scenario file could not be read",
      "The response from GitHub was not valid JSON.",
      true,
    );
  }
  return peekFailure(
    "network",
    "Could not reach GitHub",
    "Check your connection, then retry to load the scenario.",
    true,
  );
}

export function readScenarioPeekPayload(payload: unknown): PeekLoadResult {
  const parsed = parseScenarioPeek(payload);
  if (!parsed) {
    return {
      ok: false,
      error: peekFailure(
        "invalid",
        "No seeded data to show",
        "This JSON is not a Fabricate scenario with collections in state.",
        true,
      ),
    };
  }
  return {ok: true, data: parsed};
}

function failureOrIgnore(error: unknown, signal: AbortSignal): PeekLoadResult | null {
  const classified = classifyPeekException(error, signal);
  return classified ? {ok: false, error: classified} : null;
}

export async function loadScenarioPeek(rawURL: string, signal: AbortSignal): Promise<PeekLoadResult | null> {
  try {
    const response = await fetch(rawURL, {
      signal,
      headers: {Accept: "application/json"},
    });
    const httpError = classifyPeekHttpStatus(response.status);
    if (httpError) {
      return {ok: false, error: httpError};
    }
    try {
      return readScenarioPeekPayload(JSON.parse(await response.text()));
    } catch (error) {
      return failureOrIgnore(error, signal);
    }
  } catch (error) {
    return failureOrIgnore(error, signal);
  }
}
