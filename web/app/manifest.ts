import type { MetadataRoute } from "next"

// Installable like Mahaam, Zekra and Hosbah (MH-1258): a home-screen app with the Moharrik mark.
// The PNGs are rendered from app/icon.svg; maskable keeps the mark inside the 80% safe zone.
export default function manifest(): MetadataRoute.Manifest {
  return {
    id: "/",
    name: "Moharrik — محرك",
    short_name: "Moharrik",
    description: "The AI engine behind a business: automation, inbox, campaigns, CRM and research.",
    start_url: "/",
    scope: "/",
    display: "standalone",
    orientation: "any",
    background_color: "#ffffff",
    theme_color: "#00A0A8",
    categories: ["productivity", "business"],
    icons: [
      { src: "/icons/icon-192.png", sizes: "192x192", type: "image/png", purpose: "any" },
      { src: "/icons/icon-512.png", sizes: "512x512", type: "image/png", purpose: "any" },
      { src: "/icons/maskable-512.png", sizes: "512x512", type: "image/png", purpose: "maskable" },
    ],
  }
}
