/*
 * Copyright (c) 2025-2026, s0up and the autobrr contributors.
 * SPDX-License-Identifier: GPL-2.0-or-later
 */

import { useSyncExternalStore } from "react"

/**
 * Subscribe to a media query and return whether it matches.
 */
export function useMediaQuery(query: string): boolean {
  const subscribe = (callback: () => void) => {
    const mediaQuery = window.matchMedia(query)
    mediaQuery.addEventListener("change", callback)
    return () => mediaQuery.removeEventListener("change", callback)
  }

  return useSyncExternalStore(subscribe, () => window.matchMedia(query).matches)
}

/**
 * Returns true when viewport is mobile-sized (< 768px / md breakpoint).
 */
export function useIsMobile(): boolean {
  return useMediaQuery("(max-width: 767px)")
}
