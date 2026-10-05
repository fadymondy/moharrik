"use client"

import { useLeadRecord } from "@/components/leads/lead-record"
import { RecordBrief } from "@/components/record/record-brief"
import { useWorkspace } from "@/components/workspace/workspace-context"

/** MH-1169: the lead's AI context brief: who they are and how we speak to them. */
export default function LeadContextPage() {
  const { lead } = useLeadRecord()
  const ws = useWorkspace()
  return <RecordBrief collection="leads" id={lead.id} canWrite={ws.can("leads:write")} />
}
