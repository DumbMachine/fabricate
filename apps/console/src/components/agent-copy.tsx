import { useState } from "react"
import { CopyButton } from "@/components/copy-button"
import { agentBriefText, type AgentBrief } from "@/lib/agent-brief"

export function AgentCopy({ brief }: { brief: AgentBrief }) {
  const [includeSecrets, setIncludeSecrets] = useState(true)
  return (
    <div className="flex w-fit flex-col gap-1.5">
      <label className="flex items-center gap-2 text-xs text-muted-foreground">
        <input
          type="checkbox"
          className="size-3.5 accent-foreground"
          checked={includeSecrets}
          onChange={(event) => setIncludeSecrets(event.target.checked)}
        />
        Include secrets
      </label>
      <CopyButton
        value={agentBriefText(brief, includeSecrets)}
        label="Copy for agent"
      />
    </div>
  )
}
