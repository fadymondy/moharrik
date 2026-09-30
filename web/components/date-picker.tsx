"use client"

import { useState } from "react"
import { CalendarIcon, XIcon } from "lucide-react"

import { Button, ButtonGroup, Calendar, Popover, PopoverContent, PopoverTrigger } from "@fadymondy/nasaq/web"
import { useTranslations } from "@/lib/i18n"
import { dateFormat } from "@/lib/i18n-locale"
import { cn } from "@/lib/utils"

/*
  Date field (MH-487): the registry Calendar inside a Popover, instead of the browser's native
  <input type="date">, which shows mm/dd/yyyy in whatever locale the BROWSER runs, not the page's
  — so /ar showed an English American date. Ported from fadymondy.com's components/date-picker.

  The value is an ISO `YYYY-MM-DD` string (what the filters put in the address). The label is
  formatted through lib/i18n-locale, so /ar gets Arabic month names with WESTERN digits, like
  every other date in the app; the calendar grid keeps react-day-picker's default latn numerals.
*/

/** ISO date from local parts: a picked day must not shift by one in a timezone west of GMT. */
function toISO(date: Date): string {
  const p = (n: number) => String(n).padStart(2, "0")
  return `${date.getFullYear()}-${p(date.getMonth() + 1)}-${p(date.getDate())}`
}

/** Parsed as local parts: Date.parse reads a bare YYYY-MM-DD as UTC midnight. */
function fromISO(value: string | undefined): Date | undefined {
  if (!value) return undefined
  const [y, m, d] = value.split("-").map(Number)
  if (!y || !m || !d) return undefined
  return new Date(y, m - 1, d)
}

export function DatePicker({
  value,
  onValueChange,
  id,
  placeholder,
  min,
  max,
  clearable,
  className,
  disabled,
}: {
  value: string
  onValueChange: (value: string) => void
  id?: string
  placeholder?: string
  /** Earliest pickable day, ISO. */
  min?: string
  /** Latest pickable day, ISO. */
  max?: string
  /** Shows a clear button while a date is set (for optional filters). */
  clearable?: boolean
  className?: string
  disabled?: boolean
}) {
  const { t, locale } = useTranslations()
  const [open, setOpen] = useState(false)

  const selected = fromISO(value)
  const before = fromISO(min)
  const after = fromISO(max)
  const label = selected
    ? dateFormat(locale, { dateStyle: "medium" }).format(selected)
    : (placeholder ?? t("date.pick"))

  const trigger = (
    <PopoverTrigger
      render={
        <Button
          id={id}
          type="button"
          variant="secondary"
          disabled={disabled}
          data-date-picker
          data-value={value || undefined}
          className={cn(
            "min-w-0 flex-1 justify-between font-normal",
            !selected && "text-muted-foreground",
          )}
        >
          <span className="truncate">{label}</span>
          <CalendarIcon />
        </Button>
      }
    />
  )

  return (
    <Popover open={open} onOpenChange={setOpen}>
      {clearable && selected ? (
        <ButtonGroup className={cn("w-full", className)}>
          {trigger}
          <Button
            type="button"
            variant="secondary"
            size="icon"
            disabled={disabled}
            aria-label={t("date.clear")}
            onClick={() => onValueChange("")}
            data-date-clear
          >
            <XIcon />
          </Button>
        </ButtonGroup>
      ) : (
        <div className={cn("flex w-full", className)}>{trigger}</div>
      )}

      <PopoverContent align="start" className="w-auto p-0">
        <Calendar
          mode="single"
          value={selected ?? null}
          defaultMonth={selected ?? after}
          locale={locale}
          min={before}
          max={after}
          onValueChange={(date) => {
            onValueChange(date ? toISO(date) : "")
            setOpen(false)
          }}
        />
      </PopoverContent>
    </Popover>
  )
}
