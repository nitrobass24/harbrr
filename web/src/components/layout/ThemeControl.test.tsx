import { fireEvent, render, screen } from "@testing-library/react"
import { afterEach, describe, expect, it } from "vitest"
import { ThemeProvider } from "@/components/themes/theme-provider"
import { ThemeControl } from "@/components/layout/ThemeControl"

describe("ThemeControl", () => {
  afterEach(() => {
    window.localStorage.clear()
    document.documentElement.classList.remove("dark", "light")
  })

  it("flips the dark class on the html element", () => {
    render(
      <ThemeProvider>
        <ThemeControl />
      </ThemeProvider>
    )

    fireEvent.click(screen.getByLabelText("Dark theme"))
    expect(document.documentElement.classList.contains("dark")).toBe(true)

    fireEvent.click(screen.getByLabelText("Light theme"))
    expect(document.documentElement.classList.contains("dark")).toBe(false)
  })

  it("marks the active mode pressed", () => {
    render(
      <ThemeProvider>
        <ThemeControl />
      </ThemeProvider>
    )

    fireEvent.click(screen.getByLabelText("Dark theme"))
    expect(screen.getByLabelText("Dark theme").getAttribute("aria-pressed")).toBe("true")
    expect(screen.getByLabelText("Light theme").getAttribute("aria-pressed")).toBe("false")
  })
})
