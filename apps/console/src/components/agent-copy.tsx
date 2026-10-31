import { useState } from "react"
import { CopyButton } from "@/components/copy-button"
import { agentBriefText, type AgentBrief } from "@/lib/agent-brief"
export function AgentCopy({ brief }: { brief: AgentBrief }) {
  const [includeSecrets, setIncludeSecrets] = useState(false)
  return (
    <div className="flex flex-wrap items-center gap-3">
      {brief.secrets ? (
        <label className="flex items-center gap-1.5 text-xs text-muted-foreground">
          <input
            type="checkbox"
            checked={includeSecrets}
            onChange={(e) => setIncludeSecrets(e.target.checked)}
          />
          Include credentials
        </label>
      ) : null}
      <CopyButton
        value={agentBriefText(brief, includeSecrets)}
        label="Copy for agent"
      />
    </div>
  )
}
