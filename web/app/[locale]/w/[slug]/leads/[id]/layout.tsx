"use client"

import type { ReactNode } from "react"

import { LeadRecord } from "@/components/leads/lead-record"

// MH-1157: the record header and its tabs stay mounted while the tabs below change.
export default function LeadLayout({ children }: { children: ReactNode }) {
  return <LeadRecord>{children}</LeadRecord>
}
