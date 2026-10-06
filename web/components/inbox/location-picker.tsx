"use client"

import { useState } from "react"
import { MapPinIcon } from "lucide-react"

import {
  Button,
  Field,
  FieldLabel,
  Input,
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@fadymondy/nasaq/web"
import { FieldError } from "@/components/form-layout"
import { useTranslations } from "@/lib/i18n"

/*
MH-659's location control: "send my location" (browser geolocation) or manual
lat/lng entry. No map picker — no registry item for one was reachable (the
shadcn MCP is down and web/components.json has no map/leaflet piece), and a
map tile provider needs an API key, which CLAUDE.md's "no hard-coded API keys"
rules out doing ad hoc here. Manual entry with a link to open the point on
OpenStreetMap covers the same "done when" (send a location) without either.
*/

export function LocationPicker({
  disabled,
  disabledReason,
  onPick,
}: {
  disabled?: boolean
  disabledReason?: string
  onPick: (lat: number, lng: number, name: string) => void
}) {
  const { t } = useTranslations()
  const [open, setOpen] = useState(false)
  const [locating, setLocating] = useState(false)
  const [lat, setLat] = useState("")
  const [lng, setLng] = useState("")
  const [name, setName] = useState("")
  const [error, setError] = useState<string | null>(null)

  function useMine() {
    setError(null)
    if (typeof navigator === "undefined" || !navigator.geolocation) {
      setError(t("inbox.composer.locationUnavailable"))
      return
    }
    setLocating(true)
    navigator.geolocation.getCurrentPosition(
      (pos) => {
        setLocating(false)
        submit(pos.coords.latitude, pos.coords.longitude)
      },
      () => {
        setLocating(false)
        setError(t("inbox.composer.locationDenied"))
      },
      { enableHighAccuracy: true, timeout: 10000 },
    )
  }

  function submit(latVal: number, lngVal: number) {
    onPick(latVal, lngVal, name.trim())
    setOpen(false)
    setLat("")
    setLng("")
    setName("")
    setError(null)
  }

  function submitManual() {
    const latVal = Number.parseFloat(lat)
    const lngVal = Number.parseFloat(lng)
    if (
      !Number.isFinite(latVal) ||
      !Number.isFinite(lngVal) ||
      latVal < -90 ||
      latVal > 90 ||
      lngVal < -180 ||
      lngVal > 180
    ) {
      setError(t("inbox.composer.locationInvalid"))
      return
    }
    submit(latVal, lngVal)
  }

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger
        render={
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            disabled={disabled}
            title={disabled ? disabledReason : t("inbox.composer.location")}
            aria-label={t("inbox.composer.attachLocation")}
            data-action="location"
          />
        }
      >
        <MapPinIcon />
      </PopoverTrigger>
      <PopoverContent align="start" className="w-72" data-location-picker>
        <Button
          type="button"
          variant="secondary"
          size="sm"
          className="w-full"
          onClick={useMine}
          disabled={locating}
          data-action="location-mine"
        >
          <MapPinIcon />
          {locating ? t("inbox.composer.locationLocating") : t("inbox.composer.locationMine")}
        </Button>
        <div className="my-2 border-t border-nq-line" />
        <div className="flex gap-2">
          <Field className="flex-1">
            <FieldLabel htmlFor="location-lat">{t("inbox.composer.locationLat")}</FieldLabel>
            <Input
              id="location-lat"
              inputMode="decimal"
              value={lat}
              onChange={(e) => setLat(e.target.value)}
              placeholder="30.0444"
              data-location-lat
            />
          </Field>
          <Field className="flex-1">
            <FieldLabel htmlFor="location-lng">{t("inbox.composer.locationLng")}</FieldLabel>
            <Input
              id="location-lng"
              inputMode="decimal"
              value={lng}
              onChange={(e) => setLng(e.target.value)}
              placeholder="31.2357"
              data-location-lng
            />
          </Field>
        </div>
        <Field className="mt-2">
          <FieldLabel htmlFor="location-name">{t("inbox.composer.locationName")}</FieldLabel>
          <Input
            id="location-name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            dir="auto"
            data-location-name
          />
        </Field>
        {error ? <FieldError>{error}</FieldError> : null}
        <Button
          type="button"
          size="sm"
          className="mt-2 w-full"
          onClick={submitManual}
          data-action="location-use"
        >
          {t("inbox.composer.locationUse")}
        </Button>
      </PopoverContent>
    </Popover>
  )
}
