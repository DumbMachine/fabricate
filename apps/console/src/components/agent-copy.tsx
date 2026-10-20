import { useState } from "react"
import { CopyButton } from "@/components/copy-button"
import { Switch } from "@/components/motion/switch"
import { agentBriefText, type AgentBrief } from "@/lib/agent-brief"

export function AgentCopy({ brief }: { brief: AgentBrief }) {
  const [includeSecrets, setIncludeSecrets] = useState(true)
  return (
    <div className="flex w-fit flex-col items-start gap-2">
      <Switch
        checked={includeSecrets}
        onCheckedChange={setIncludeSecrets}
        label="Include secrets"
      />
      <CopyButton
        value={agentBriefText(brief, includeSecrets)}
        label="Copy for agent"
      />
    </div>
  )
}
