"use client"

import { useRef } from "react"
import { FileIcon, ImageIcon, PaperclipIcon } from "lucide-react"

import { Button, DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@fadymondy/nasaq/web"
import { LocationPicker } from "@/components/inbox/location-picker"
import { useTranslations } from "@/lib/i18n"

export type PickedFile = { kind: "image" | "video" | "voice" | "document"; file: File }

/**
 * MH-672: the kind a dropped or pasted file goes out as. Audio is sent as a voice note so it
 * plays inline on both sides, like one recorded with the mic; anything unrecognised is a document.
 */
export function pickedFromFile(file: File): PickedFile {
  const type = file.type
  if (type.startsWith("image/")) return { kind: "image", file }
  if (type.startsWith("video/")) return { kind: "video", file }
  if (type.startsWith("audio/")) return { kind: "voice", file }
  return { kind: "document", file }
}

/** MH-659's attach button: photo/video, document, or a location — one at a time. */
export function AttachMenu({
  disabled,
  disabledReason,
  onPickFile,
  onPickLocation,
}: {
  disabled?: boolean
  disabledReason?: string
  onPickFile: (picked: PickedFile) => void
  onPickLocation: (lat: number, lng: number, name: string) => void
}) {
  const { t } = useTranslations()
  const mediaInputRef = useRef<HTMLInputElement>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)

  return (
    <div className="flex items-center">
      <DropdownMenu>
        <DropdownMenuTrigger
          render={
            <Button
              type="button"
              variant="ghost"
              size="icon-sm"
              disabled={disabled}
              title={disabled ? disabledReason : t("inbox.composer.attach")}
              aria-label={t("inbox.composer.attach")}
              data-action="attach"
            />
          }
        >
          <PaperclipIcon />
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start" className="w-auto min-w-44 whitespace-nowrap">
          <DropdownMenuItem onClick={() => mediaInputRef.current?.click()} data-attach="media">
            <ImageIcon />
            {t("inbox.composer.attachPhotoVideo")}
          </DropdownMenuItem>
          <DropdownMenuItem onClick={() => fileInputRef.current?.click()} data-attach="document">
            <FileIcon />
            {t("inbox.composer.attachDocument")}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      <LocationPicker disabled={disabled} disabledReason={disabledReason} onPick={onPickLocation} />
      <input
        ref={mediaInputRef}
        type="file"
        accept="image/*,video/*"
        className="hidden"
        data-testid="attach-media-input"
        onChange={(e) => {
          const file = e.target.files?.[0]
          e.target.value = ""
          if (!file) return
          onPickFile({ kind: file.type.startsWith("video/") ? "video" : "image", file })
        }}
      />
      <input
        ref={fileInputRef}
        type="file"
        className="hidden"
        data-testid="attach-document-input"
        onChange={(e) => {
          const file = e.target.files?.[0]
          e.target.value = ""
          if (!file) return
          onPickFile({ kind: "document", file })
        }}
      />
    </div>
  )
}
