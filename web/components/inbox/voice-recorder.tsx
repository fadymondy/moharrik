"use client"

import { useEffect, useRef, useState } from "react"
import { MicIcon, SendIcon, XIcon } from "lucide-react"

import { Button } from "@/components/ui/button"
import { useTranslations } from "@/lib/i18n"

/*
MH-659's mic button. Records with MediaRecorder, preferring ogg/opus — the
format WhatsApp's own voice notes use — and falling back to webm/opus where
ogg isn't supported (Safari has neither reliably; Chrome/Edge/Firefox on
desktop and Android do ogg). Whatever the browser produces is sent as-is:
Evolution's sendWhatsAppAudio takes base64 audio bytes and does not require a
specific container on this side, and the SMTP path (internal/marketing/mail.go)
attaches whatever bytes and content-type it is given.
*/

function pickMimeType(): string {
  const candidates = ["audio/ogg;codecs=opus", "audio/ogg", "audio/webm;codecs=opus", "audio/webm"]
  for (const c of candidates) {
    if (typeof MediaRecorder !== "undefined" && MediaRecorder.isTypeSupported?.(c)) return c
  }
  return ""
}

function formatTimer(ms: number): string {
  const s = Math.floor(ms / 1000)
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`
}

export function VoiceRecorder({
  disabled,
  disabledReason,
  onRecorded,
}: {
  disabled?: boolean
  disabledReason?: string
  onRecorded: (blob: Blob, mimeType: string, durationMs: number) => void
}) {
  const { t } = useTranslations()
  const [recording, setRecording] = useState(false)
  const [elapsed, setElapsed] = useState(0)
  const [error, setError] = useState<string | null>(null)
  const recorderRef = useRef<MediaRecorder | null>(null)
  const chunksRef = useRef<Blob[]>([])
  const streamRef = useRef<MediaStream | null>(null)
  const startRef = useRef(0)
  const timerRef = useRef<number | null>(null)
  const cancelledRef = useRef(false)

  useEffect(
    () => () => {
      stopTracks()
      if (timerRef.current) window.clearInterval(timerRef.current)
    },
    [],
  )

  function stopTracks() {
    streamRef.current?.getTracks().forEach((tr) => tr.stop())
    streamRef.current = null
  }

  async function start() {
    setError(null)
    if (typeof navigator === "undefined" || !navigator.mediaDevices?.getUserMedia || typeof MediaRecorder === "undefined") {
      setError(t("inbox.composer.micUnavailable"))
      return
    }
    let stream: MediaStream
    try {
      stream = await navigator.mediaDevices.getUserMedia({ audio: true })
    } catch {
      setError(t("inbox.composer.micNoPermission"))
      return
    }
    streamRef.current = stream
    const mimeType = pickMimeType()
    const recorder = mimeType ? new MediaRecorder(stream, { mimeType }) : new MediaRecorder(stream)
    chunksRef.current = []
    cancelledRef.current = false
    recorder.ondataavailable = (e) => {
      if (e.data.size > 0) chunksRef.current.push(e.data)
    }
    recorder.onstop = () => {
      stopTracks()
      if (!cancelledRef.current && chunksRef.current.length > 0) {
        const blob = new Blob(chunksRef.current, { type: recorder.mimeType || mimeType || "audio/webm" })
        onRecorded(blob, blob.type, Date.now() - startRef.current)
      }
      chunksRef.current = []
    }
    recorderRef.current = recorder
    recorder.start()
    startRef.current = Date.now()
    setElapsed(0)
    setRecording(true)
    timerRef.current = window.setInterval(() => setElapsed(Date.now() - startRef.current), 200)
  }

  function finish(cancel: boolean) {
    cancelledRef.current = cancel
    if (timerRef.current) {
      window.clearInterval(timerRef.current)
      timerRef.current = null
    }
    setRecording(false)
    recorderRef.current?.stop()
    recorderRef.current = null
  }

  if (recording) {
    return (
      <div
        className="flex flex-1 items-center gap-2 rounded-md border border-line bg-grid-elevated px-2 py-1"
        data-recording-bar
      >
        <Button
          type="button"
          variant="ghost"
          size="icon-sm"
          onClick={() => finish(true)}
          aria-label={t("inbox.composer.recordingCancel")}
          data-action="recording-cancel"
        >
          <XIcon />
        </Button>
        <span className="flex items-center gap-1.5 text-sm text-grid-fg" aria-live="polite">
          <span className="size-2 animate-pulse rounded-full bg-grid-danger-text" aria-hidden />
          <span data-recording-timer>{formatTimer(elapsed)}</span>
          <span className="text-xs text-grid-muted">{t("inbox.composer.recordingHint")}</span>
        </span>
        <Button
          type="button"
          size="icon-sm"
          className="ms-auto"
          onClick={() => finish(false)}
          aria-label={t("inbox.composer.recordingSend")}
          data-action="recording-send"
        >
          <SendIcon />
        </Button>
      </div>
    )
  }

  return (
    <div className="flex items-center gap-1">
      <Button
        type="button"
        variant="ghost"
        size="icon-sm"
        disabled={disabled}
        onClick={start}
        title={disabled ? disabledReason : t("inbox.composer.mic")}
        aria-label={t("inbox.composer.mic")}
        data-action="mic"
      >
        <MicIcon />
      </Button>
      {error ? (
        <span className="text-xs text-grid-danger-text" role="alert">
          {error}
        </span>
      ) : null}
    </div>
  )
}
