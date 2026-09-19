"use client"

import Script from "next/script"

import { useTranslations } from "@/lib/i18n"

declare global {
  interface Window {
    MahaamFeedback?: { open: () => void; close: () => void; show: () => void }
  }
}

// Mahaam's embeddable feedback widget (console.mahaam.app/embed/v1.js): the "Report a problem"
// dialog — screenshot, element picker, console/network capture, type and priority.
//
// It posts to OUR /feedback route (app/feedback/route.ts), not to Mahaam directly: that route
// checks the session, adds the workspace, account and host, attaches Moharrik's real project key
// server-side and forwards to Mahaam's /api/feedback/embed. The key never ships to the browser;
// the data-key here is only the widget's "configured" marker.
const EMBED = process.env.NEXT_PUBLIC_MAHAAM_EMBED_ORIGIN ?? "https://console.mahaam.app"

export function MahaamFeedbackWidget() {
  const { locale } = useTranslations()
  return (
    <Script
      id="mahaam-feedback"
      src={`${EMBED}/embed/v1.js`}
      strategy="afterInteractive"
      data-key="pfk_moharrik"
      data-endpoint="/feedback"
      data-locale={locale}
      data-launcher="false"
    />
  )
}

/** Opens the widget (a no-op until it has loaded). */
export function openFeedback() {
  window.MahaamFeedback?.open()
}
