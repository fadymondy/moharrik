"use client"

import { useState } from "react"
import { CheckIcon, CopyIcon } from "lucide-react"
import { toast } from "@fadymondy/nasaq/web"

import { Button } from "@/components/ui/button"
import { useTranslations } from "@/lib/i18n"

/** A one-time secret or link: mono, always LTR, with a copy button. */
export function CopyField({
  value,
  label,
  nowrap = false,
  pre = false,
}: {
  value: string
  label?: string
  /** One line that scrolls inside the field, never broken mid-token: an address, a slug, a checksum. */
  nowrap?: boolean
  /** Formatted code (JSON): its own line breaks and indentation, scrolling sideways and down, never re-wrapped. */
  pre?: boolean
}) {
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
    <div className="flex items-stretch border border-nq-line bg-card">
      <code
        dir="ltr"
        aria-label={label}
        className={`ltr-isolate min-w-0 flex-1 overflow-x-auto px-3 py-2 font-mono text-xs leading-5 text-foreground select-all ${pre ? "max-h-96 overflow-y-auto whitespace-pre" : nowrap ? "whitespace-nowrap" : "break-all whitespace-pre-wrap"}`}
      >
        {value}
      </code>
      <Button
        type="button"
        variant="ghost"
        className="h-auto shrink-0 rounded-none border-s border-nq-line px-3"
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
export function Ltr({
  children,
  mono = false,
  className = "",
}: {
  children: React.ReactNode
  mono?: boolean
  className?: string
}) {
  return (
    <span dir="ltr" className={`ltr-isolate ${mono ? "font-mono" : ""} ${className}`}>
      {children}
    </span>
  )
}
