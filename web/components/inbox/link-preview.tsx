"use client"

import { useWorkspace } from "@/components/workspace/workspace-context"
import { useLinkPreview } from "@/lib/queries"
import { useTranslations } from "@/lib/i18n"

/*
The card WhatsApp shows inside a bubble whose text carries a URL (MH-656):
the page's image, title, description and site. Fetched by the server
(GET /api/w/link_preview — private-network guarded and cached), so the
customer's link is never opened from the member's browser just to draw it;
only the image loads from its own host, with no referrer. A page with nothing
worth a card, or one that could not be read, shows no card at all.
*/

export function LinkPreviewCard({ url }: { url: string }) {
  const ws = useWorkspace()
  const { t } = useTranslations()
  const { data } = useLinkPreview(ws.id, url)
  if (!data || data.status !== "ok") return null
  const site = data.site_name || hostOf(url)

  return (
    <a
      href={url}
      target="_blank"
      rel="noreferrer noopener"
      aria-label={`${t("inbox.thread.linkPreview")}: ${data.title || site}`}
      className="mb-1.5 block overflow-hidden rounded-md bg-[color-mix(in_oklab,var(--grid-fg)_6%,transparent)] no-underline transition-colors hover:bg-[color-mix(in_oklab,var(--grid-fg)_10%,transparent)]"
      data-link-preview
    >
      {data.image_url ? (
        // A remote page's image: next/image would need every host allow-listed.
        // eslint-disable-next-line @next/next/no-img-element
        <img
          src={data.image_url}
          alt=""
          referrerPolicy="no-referrer"
          loading="lazy"
          className="max-h-56 w-full object-cover"
          onError={(e) => {
            e.currentTarget.style.display = "none"
          }}
        />
      ) : null}
      <div className="flex flex-col gap-0.5 px-2.5 py-2" dir="auto">
        {data.title ? (
          <p className="line-clamp-2 text-sm font-medium text-grid-fg">{data.title}</p>
        ) : null}
        {data.description ? (
          <p className="line-clamp-2 text-xs text-grid-muted">{data.description}</p>
        ) : null}
        <p className="truncate text-xs text-grid-muted" dir="ltr">
          {site}
        </p>
      </div>
    </a>
  )
}

function hostOf(url: string): string {
  try {
    return new URL(url).hostname.replace(/^www\./, "")
  } catch {
    return url
  }
}
