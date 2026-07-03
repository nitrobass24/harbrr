import { ThemeProvider as NextThemesProvider } from "next-themes"
import type { ReactNode } from "react"

// Light / Dark / System via next-themes (class strategy). Default is System
// (docs/webui-scope.md §9 decision 5); the dark palette is the mockup's.
export function ThemeProvider({ children }: { children: ReactNode }) {
  return (
    <NextThemesProvider attribute="class" defaultTheme="system" enableSystem disableTransitionOnChange>
      {children}
    </NextThemesProvider>
  )
}
