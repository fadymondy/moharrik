"use client"

import type { ReactNode } from "react"

import { ContactRecord } from "@/components/contacts/contact-record"

// MH-1157: the record header and its tabs stay mounted while the tabs below change.
export default function ContactLayout({ children }: { children: ReactNode }) {
  return <ContactRecord>{children}</ContactRecord>
}
