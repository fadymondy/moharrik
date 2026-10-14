"use client"

import type { ReactNode } from "react"

import { AuthFooter, AuthLayout } from "@fadymondy/nasaq/web"
import { LanguageSwitcher, ThemeToggle } from "@/components/header-controls"

/** The signed-out card the sign-in page uses, shared by the password-reset and verify-email pages. */
export function AuthShell({
  title,
  description,
  children,
}: {
  title: string
  description?: string
  children: ReactNode
}) {
  return (
    <AuthLayout
      title={title}
      description={description}
      footer={
        <AuthFooter
          end={
            <div className="flex items-center gap-1">
              <LanguageSwitcher />
              <ThemeToggle />
            </div>
          }
        />
      }
    >
      <div className="w-full">{children}</div>
    </AuthLayout>
  )
}
