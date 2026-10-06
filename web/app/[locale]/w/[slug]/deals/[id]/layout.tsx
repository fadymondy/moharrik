"use client"

import type { ReactNode } from "react"

import { DealRecord } from "@/components/deals/deal-record"

// MH-1161: the record header and its tabs stay mounted while the tabs below change.
export default function DealLayout({ children }: { children: ReactNode }) {
  return <DealRecord>{children}</DealRecord>
}
