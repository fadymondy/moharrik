import {
  FacebookIcon,
  GlobeIcon,
  HashIcon,
  InstagramIcon,
  LinkedinIcon,
  MailIcon,
  MessageCircleIcon,
  MessageSquareIcon,
  SendIcon,
  SlackIcon,
  type LucideIcon,
} from "lucide-react"

/*
Shared with the inbox list/tabs (MH-568) and the WhatsApp-style thread (MH-587):
one place naming an icon and a label for a channel, so the tab strip, the
per-message channel badge and the floating launcher agree on both. Channel
tabs stay data-driven from channel_counts — this map is only a nicer icon for
the channels this build happens to know about; an unlisted one still renders,
just with GlobeIcon and its raw, capitalised name.
*/

const CHANNEL_ICONS: Record<string, LucideIcon> = {
  chat: MessageSquareIcon,
  email: MailIcon,
  whatsapp: MessageCircleIcon,
  telegram: SendIcon,
  slack: SlackIcon,
  discord: HashIcon,
  facebook: FacebookIcon,
  linkedin: LinkedinIcon,
  instagram: InstagramIcon,
}

export function channelIcon(channel: string): LucideIcon {
  return CHANNEL_ICONS[channel] ?? GlobeIcon
}

/** t() falls back to the raw key when a translation is missing (web/lib/i18n.tsx). */
export function channelLabel(channel: string, t: (key: string) => string): string {
  const key = `inbox.channel.${channel}`
  const translated = t(key)
  return translated === key ? channel.charAt(0).toUpperCase() + channel.slice(1) : translated
}
