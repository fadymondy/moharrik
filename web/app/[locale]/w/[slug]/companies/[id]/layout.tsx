"use client"

import type { ReactNode } from "react"

import { CompanyRecord } from "@/components/companies/company-record"

// MH-1157: the record header and its tabs stay mounted while the tabs below change.
export default function CompanyLayout({ children }: { children: ReactNode }) {
  return <CompanyRecord>{children}</CompanyRecord>
}
