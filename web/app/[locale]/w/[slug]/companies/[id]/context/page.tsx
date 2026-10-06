"use client"

import { useCompanyRecord } from "@/components/companies/company-record"
import { RecordBrief } from "@/components/record/record-brief"

/** MH-1169: the company's AI context brief: who they are and how we speak to them. */
export default function CompanyContextPage() {
  const { company, canWrite } = useCompanyRecord()
  return <RecordBrief collection="companies" id={company.id} canWrite={canWrite} />
}
