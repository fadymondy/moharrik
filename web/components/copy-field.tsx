"use client"

import { useState } from "react"
import { CheckIcon, CopyIcon } from "lucide-react"
import { toast } from "sonner"

import { Button } from "@/components/ui/button"
import { useTranslations } from "@/lib/i18n"

/** A one-time secret or link: mono, always LTR, with a copy button. */
export function CopyField({ value, label }: { value: string; label?: string }) {
  const { t } = useTranslations()
  const [copied, setCopied] = useState(false)

  async function copy() {
    try {
      await navigator.clipboard.writeText(value)
      setCopied(true)
      toast.success(t("common.copied"))
      setTimeout(() => setCopied(false), 1800)
    } catch {
      toast.error(t("common.copyFailed"))
    }
  }

  return (
    <div className="flex items-stretch border border-line bg-grid-card">
      <code
        dir="ltr"
        aria-label={label}
        className="ltr-isolate min-w-0 flex-1 overflow-x-auto px-3 py-2 font-mono text-xs leading-5 break-all whitespace-pre-wrap text-grid-fg select-all"
      >
        {value}
      </code>
      <Button
        type="button"
        variant="ghost"
        className="h-auto shrink-0 rounded-none border-s border-line px-3"
        onClick={copy}
        aria-label={t("common.copy")}
      >
        {copied ? <CheckIcon /> : <CopyIcon />}
        <span className="hidden sm:inline">{copied ? t("common.copied") : t("common.copy")}</span>
      </Button>
    </div>
  )
}

/** Codes, emails, numbers and dates keep LTR order inside RTL text. */
export function Ltr({ children, mono = false, className = "" }: { children: React.ReactNode; mono?: boolean; className?: string }) {
  return (
    <span dir="ltr" className={`ltr-isolate ${mono ? "font-mono" : ""} ${className}`}>
      {children}
    </span>
  )
}
