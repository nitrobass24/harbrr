import { act, renderHook } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import { useIsMobile, useMediaQuery } from "@/hooks/useMediaQuery"

// A minimal MediaQueryList stub whose `matches` and listeners we control per test.
function stubMatchMedia(initialMatches: boolean) {
  let matches = initialMatches
  let listener: (() => void) | undefined

  const mql = {
    get matches() {
      return matches
    },
    media: "",
    addEventListener: (_: string, cb: () => void) => {
      listener = cb
    },
    removeEventListener: () => {
      listener = undefined
    },
  }

  vi.stubGlobal("matchMedia", vi.fn().mockReturnValue(mql))

  return {
    change(next: boolean) {
      matches = next
      act(() => listener?.())
    },
  }
}

describe("useMediaQuery", () => {
  afterEach(() => vi.unstubAllGlobals())

  it("returns the initial match state", () => {
    stubMatchMedia(true)
    const { result } = renderHook(() => useMediaQuery("(max-width: 767px)"))
    expect(result.current).toBe(true)
  })

  it("updates when the media query change event fires", () => {
    const media = stubMatchMedia(false)
    const { result } = renderHook(() => useMediaQuery("(max-width: 767px)"))
    expect(result.current).toBe(false)

    media.change(true)
    expect(result.current).toBe(true)
  })
})

describe("useIsMobile", () => {
  afterEach(() => vi.unstubAllGlobals())

  it("is true under the md breakpoint", () => {
    stubMatchMedia(true)
    const { result } = renderHook(() => useIsMobile())
    expect(result.current).toBe(true)
  })

  it("is false at/above the md breakpoint", () => {
    stubMatchMedia(false)
    const { result } = renderHook(() => useIsMobile())
    expect(result.current).toBe(false)
  })
})
