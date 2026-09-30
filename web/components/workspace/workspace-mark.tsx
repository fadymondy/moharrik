"use client"

import { useEffect, useState } from "react"

import { cn } from "@/lib/utils"

/*
A workspace's mark (MH-679): its uploaded picture, or its first letter on the
card tile when it has none — or when the picture fails to load, so a broken
image never shows. Square, like every tile in the grid system; the same one in
the switcher, the workspaces list and Settings, so a team is recognisable at a
glance wherever it appears.
*/

const SIZES = {
  sm: "size-6 text-xs",
  md: "size-9 text-sm",
  lg: "size-16 text-2xl",
} as const

export function WorkspaceMark({
  name,
  avatarUrl,
  size = "md",
  className,
}: {
  name: string
  avatarUrl?: string | null
  size?: keyof typeof SIZES
  className?: string
}) {
  const [failed, setFailed] = useState(false)
  useEffect(() => setFailed(false), [avatarUrl])
  const showImage = !!avatarUrl && !failed

  return (
    <span
      className={cn(
        "flex shrink-0 items-center justify-center overflow-hidden border border-nq-line bg-card font-medium text-nq-brand",
        SIZES[size],
        className,
      )}
      data-workspace-mark={showImage ? "image" : "initial"}
      aria-hidden
    >
      {showImage ? (
        // eslint-disable-next-line @next/next/no-img-element -- a same-origin, cookie-authorised API image; next/image would proxy it without the session
        <img
          src={avatarUrl!}
          alt=""
          className="size-full object-cover"
          onError={() => setFailed(true)}
        />
      ) : (
        (name.trim().slice(0, 1) || "?").toUpperCase()
      )}
    </span>
  )
}

export const WORKSPACE_AVATAR_TYPES = ["image/png", "image/jpeg", "image/webp"]
const MAX_EDGE = 512

/**
 * Reads a picked image and returns it ready for PUT /api/w/workspace/avatar:
 * scaled down to 512px on its longer edge (a mark never shows bigger), so even
 * a large photo uploads as a few dozen KB. Smaller images go up untouched.
 */
export async function prepareWorkspaceAvatar(
  file: File,
): Promise<{ data: string; content_type: string }> {
  const bitmap = await createImageBitmap(file)
  const scale = Math.min(1, MAX_EDGE / Math.max(bitmap.width, bitmap.height))
  let blob: Blob = file
  if (scale < 1) {
    const canvas = document.createElement("canvas")
    canvas.width = Math.round(bitmap.width * scale)
    canvas.height = Math.round(bitmap.height * scale)
    canvas.getContext("2d")!.drawImage(bitmap, 0, 0, canvas.width, canvas.height)
    blob = await new Promise<Blob>((resolve, reject) =>
      canvas.toBlob(
        (b) => (b ? resolve(b) : reject(new Error("encode failed"))),
        "image/webp",
        0.9,
      ),
    )
  }
  bitmap.close()
  const buf = new Uint8Array(await blob.arrayBuffer())
  let bin = ""
  for (let i = 0; i < buf.length; i += 0x8000) {
    bin += String.fromCharCode(...buf.subarray(i, i + 0x8000))
  }
  // A browser without WEBP encoding hands back PNG; trust the blob's own type.
  return { data: btoa(bin), content_type: blob.type || file.type }
}
