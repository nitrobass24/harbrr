import { useState } from "react"
import { fireEvent, render, screen } from "@testing-library/react"
import { describe, expect, it } from "vitest"
import { HostPortFields } from "./HostPortFields"

// A thin controlled harness — HostPortFields is a pure controlled component, so
// exercising the paste/type behavior needs real state driving it back.
function Harness({ showScheme = true }: { showScheme?: boolean }) {
  const [scheme, setScheme] = useState<"http" | "https">("http")
  const [host, setHost] = useState("")
  const [port, setPort] = useState("8080")
  return (
    <HostPortFields
      idPrefix="t"
      scheme={scheme}
      host={host}
      port={port}
      onScheme={setScheme}
      onHost={setHost}
      onPort={setPort}
      showScheme={showScheme}
    />
  )
}

function paste(el: HTMLElement, text: string) {
  fireEvent.paste(el, { clipboardData: { getData: () => text } })
}

describe("HostPortFields paste handling", () => {
  it("pasting a full URL fans out into scheme, host, and port", () => {
    render(<Harness />)
    paste(screen.getByLabelText("Host"), "https://sonarr:8989")

    expect(screen.getByLabelText<HTMLSelectElement>("Scheme").value).toBe("https")
    expect(screen.getByLabelText<HTMLInputElement>("Host").value).toBe("sonarr")
    expect(screen.getByLabelText<HTMLInputElement>("Port").value).toBe("8989")
  })

  it("pasting a bare host:port fans out host+port when scheme is hidden (Deluge)", () => {
    render(<Harness showScheme={false} />)
    paste(screen.getByLabelText("Host"), "localhost:58846")

    expect(screen.getByLabelText<HTMLInputElement>("Host").value).toBe("localhost")
    expect(screen.getByLabelText<HTMLInputElement>("Port").value).toBe("58846")
  })

  it("pasting a bare host:port fans out host+port even when scheme is shown", () => {
    render(<Harness showScheme />)
    paste(screen.getByLabelText("Host"), "localhost:58846")

    expect(screen.getByLabelText<HTMLInputElement>("Host").value).toBe("localhost")
    expect(screen.getByLabelText<HTMLInputElement>("Port").value).toBe("58846")
  })

  it("typing a URL character-by-character does not fan out — it's onPaste-only", () => {
    render(<Harness />)
    const host = screen.getByLabelText<HTMLInputElement>("Host")

    // fireEvent.change simulates the DOM state after each keystroke, same as a real
    // controlled input firing onChange per character.
    fireEvent.change(host, { target: { value: "h" } })
    fireEvent.change(host, { target: { value: "ht" } })
    fireEvent.change(host, { target: { value: "http" } })
    fireEvent.change(host, { target: { value: "http:" } })
    fireEvent.change(host, { target: { value: "http:/" } })
    fireEvent.change(host, { target: { value: "http://" } })
    fireEvent.change(host, { target: { value: "http://s" } })

    expect(host.value).toBe("http://s")
    // The seeded port default is untouched — typing never routes to it.
    expect(screen.getByLabelText<HTMLInputElement>("Port").value).toBe("8080")
  })

  it("pasting a fragment into a non-empty, partially-selected host does not fan out", () => {
    render(<Harness />)
    const host = screen.getByLabelText<HTMLInputElement>("Host")
    fireEvent.change(host, { target: { value: "existing-host" } })

    // Cursor placed mid-string (not selecting the whole field) — a real fragment paste,
    // not a full-address paste, so it must fall through to a literal insert untouched.
    host.setSelectionRange(3, 3)
    paste(host, "localhost:58846")

    expect(host.value).toBe("existing-host")
    expect(screen.getByLabelText<HTMLInputElement>("Port").value).toBe("8080")
  })
})
