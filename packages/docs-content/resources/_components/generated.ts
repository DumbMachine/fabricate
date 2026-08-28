import fs from "node:fs";
import path from "node:path";
import {fileURLToPath} from "node:url";

const generatedDir = fileURLToPath(new URL("../_generated/", import.meta.url));
const snapshotName = /^[a-zA-Z0-9._-]+$/;

export type CommandExample = {
  command: string;
  environment: {label: string; manifest: string};
  id: string;
  output: unknown;
  title: string;
};

export type OperationStatus = "passed" | "failed" | "pending";

export type Compatibility = {
  api: string;
  environment: {label: string; manifest: string; messages: number};
  integration: string;
  modes: Record<string, {
    messagesAfter?: number;
    messagesBefore?: number;
    operations: Record<string, OperationStatus>;
    status: OperationStatus;
  }>;
  operationLabels: Record<string, string>;
  testedAt: string;
  testedCommit: string;
  verification: {
    client: string;
    kind: string;
    label: string;
    title: string;
  };
};

export type Operation = {
  method: string;
  path: string;
  operationId: string;
  summary?: string;
};

export type OperationsCatalog = {
  api: string;
  integration: string;
  operations: Operation[];
};

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function isStatus(value: unknown): value is OperationStatus {
  return value === "passed" || value === "failed" || value === "pending";
}

export function readGeneratedJSON(filename: string): unknown | null {
  if (!snapshotName.test(filename)) {
    return null;
  }
  const dest = path.resolve(generatedDir, filename);
  const root = path.resolve(generatedDir);
  if (dest !== root && !dest.startsWith(root + path.sep)) {
    return null;
  }
  try {
    return JSON.parse(fs.readFileSync(dest, "utf8")) as unknown;
  } catch {
    return null;
  }
}

export function parseCommandExample(value: unknown): CommandExample | null {
  if (!isRecord(value)) {
    return null;
  }
  const environment = isRecord(value.environment) ? value.environment : null;
  if (
    typeof value.id !== "string" ||
    typeof value.title !== "string" ||
    typeof value.command !== "string" ||
    !environment ||
    typeof environment.label !== "string" ||
    typeof environment.manifest !== "string" ||
    !("output" in value)
  ) {
    return null;
  }
  return {
    id: value.id,
    title: value.title,
    command: value.command,
    environment: {label: environment.label, manifest: environment.manifest},
    output: value.output,
  };
}

export function parseCompatibility(value: unknown): Compatibility | null {
  if (!isRecord(value)) {
    return null;
  }
  const environment = isRecord(value.environment) ? value.environment : null;
  const verification = isRecord(value.verification) ? value.verification : null;
  const modesIn = isRecord(value.modes) ? value.modes : null;
  const labelsIn = isRecord(value.operationLabels) ? value.operationLabels : null;
  if (
    typeof value.api !== "string" ||
    typeof value.integration !== "string" ||
    typeof value.testedAt !== "string" ||
    typeof value.testedCommit !== "string" ||
    !environment ||
    typeof environment.label !== "string" ||
    typeof environment.manifest !== "string" ||
    typeof environment.messages !== "number" ||
    !verification ||
    typeof verification.client !== "string" ||
    typeof verification.kind !== "string" ||
    typeof verification.label !== "string" ||
    typeof verification.title !== "string" ||
    !modesIn ||
    !labelsIn
  ) {
    return null;
  }
  const operationLabels: Record<string, string> = {};
  for (const [key, label] of Object.entries(labelsIn)) {
    if (typeof label === "string") {
      operationLabels[key] = label;
    }
  }
  const modes: Compatibility["modes"] = {};
  for (const [name, raw] of Object.entries(modesIn)) {
    if (!isRecord(raw) || !isStatus(raw.status) || !isRecord(raw.operations)) {
      continue;
    }
    const operations: Record<string, OperationStatus> = {};
    for (const [key, status] of Object.entries(raw.operations)) {
      if (isStatus(status)) {
        operations[key] = status;
      }
    }
    modes[name] = {
      status: raw.status,
      operations,
      messagesAfter: typeof raw.messagesAfter === "number" ? raw.messagesAfter : undefined,
      messagesBefore: typeof raw.messagesBefore === "number" ? raw.messagesBefore : undefined,
    };
  }
  if (Object.keys(modes).length === 0 || Object.keys(operationLabels).length === 0) {
    return null;
  }
  return {
    api: value.api,
    integration: value.integration,
    testedAt: value.testedAt,
    testedCommit: value.testedCommit,
    environment: {
      label: environment.label,
      manifest: environment.manifest,
      messages: environment.messages,
    },
    verification: {
      client: verification.client,
      kind: verification.kind,
      label: verification.label,
      title: verification.title,
    },
    modes,
    operationLabels,
  };
}

export function parseOperationsCatalog(value: unknown): OperationsCatalog | null {
  if (!isRecord(value) || typeof value.api !== "string" || typeof value.integration !== "string" || !Array.isArray(value.operations)) {
    return null;
  }
  const operations: Operation[] = [];
  for (const item of value.operations) {
    if (!isRecord(item) || typeof item.method !== "string" || typeof item.path !== "string" || typeof item.operationId !== "string") {
      continue;
    }
    operations.push({
      method: item.method,
      path: item.path,
      operationId: item.operationId,
      summary: typeof item.summary === "string" ? item.summary : undefined,
    });
  }
  if (operations.length === 0) {
    return null;
  }
  return {api: value.api, integration: value.integration, operations};
}

export function loadCommandExample(id?: string, provided?: unknown): CommandExample | null {
  return parseCommandExample(provided) ?? (id ? parseCommandExample(readGeneratedJSON(`${id}.json`)) : null);
}

export function loadCompatibility(resource?: string, provided?: unknown): Compatibility | null {
  return parseCompatibility(provided) ?? (resource ? parseCompatibility(readGeneratedJSON(`${resource}.compatibility.json`)) : null);
}

export function loadOperationsCatalog(resource?: string, provided?: unknown): OperationsCatalog | null {
  return parseOperationsCatalog(provided) ?? (resource ? parseOperationsCatalog(readGeneratedJSON(`${resource}.operations.json`)) : null);
}
