import { configure } from "@testing-library/react"

// The shared self-hosted CI runners can stall a render well past testing-library's
// 1s async default, flaking findBy*/waitFor on load (AnnounceSection's edit button
// sank two unrelated PR runs in one day). 4s absorbs runner contention; genuinely
// missing elements still fail, just slower.
configure({ asyncUtilTimeout: 4000 })

// jsdom lacks matchMedia; next-themes needs it to resolve the system theme.
Object.defineProperty(window, "matchMedia", {
  writable: true,
  value: (query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => false,
  }),
})

// jsdom lacks ResizeObserver; Radix's Checkbox measures itself with one when
// rendered inside a <form> (for its hidden form-submission bubble input).
class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
window.ResizeObserver = ResizeObserverStub
