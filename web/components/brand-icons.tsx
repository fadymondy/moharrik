/*
Brand marks that used to ship inside lucide-react (<1.0) and were removed in
1.0: Youtube, Facebook, Instagram, Linkedin, Slack. This file is the one place
that now supplies them, so a screen imports `YoutubeIcon` from here exactly as
it used to import it from lucide-react.

Each export is a real `LucideIcon` (built with Lucide's own createLucideIcon:
same props, same `size`/`className` handling), so it fits anywhere a Lucide
icon is expected: nav items, the inbox channel map, `icon={<YoutubeIcon />}`.

- YouTube, Facebook, Instagram: the real glyph from simple-icons (CC0),
  filled with currentColor so it follows the text colour like the old ones.
- Slack, LinkedIn: simple-icons no longer carries either logo (both pulled
  from the package after takedown requests, same as lib/brands.ts says), and
  we do not hand-copy a trademark path. They get a neutral Lucide glyph, the
  same call lib/brands.ts already makes for LinkedIn.
*/

import { siFacebook, siInstagram, siYoutube, type SimpleIcon } from "simple-icons"
import { BlocksIcon, BriefcaseBusinessIcon, createLucideIcon } from "lucide-react"

/** A simple-icons glyph as a filled Lucide icon: no stroke, painted in currentColor. */
function glyph(name: string, icon: SimpleIcon) {
  return createLucideIcon(name, [
    ["path", { d: icon.path, fill: "currentColor", stroke: "none", key: name }],
  ])
}

export const YoutubeIcon = glyph("youtube", siYoutube)
export const FacebookIcon = glyph("facebook", siFacebook)
export const InstagramIcon = glyph("instagram", siInstagram)

// No logo available: neutral stand-ins (see the note at the top).
export const SlackIcon = BlocksIcon
export const LinkedinIcon = BriefcaseBusinessIcon
