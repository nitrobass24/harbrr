import { toast } from "sonner"
import { api } from "@/lib/api"

// notify.ts is the ONE place the web UI is allowed to import "sonner" for toast.* calls
// (components/ui/sonner.tsx, the <Toaster/> mount, is the only other exception — it
// renders sonner's Toaster component, it never calls toast()). Every call site uses
// notifyError/notifyWarn/notifySuccess/notifyInfo instead of toast.* directly, so every
// error/warning toast is also relayed into the daemon's own log (harbrr#112): the daemon
// log is THE log for single-user self-hosted software, but a toast often describes a
// client-only event (a fetch that never reached the server) the server never otherwise
// sees. notifySuccess/notifyInfo are plain UI wrappers — only error/warn ship, per the
// issue's emphasis, to keep the shipping set small and avoid noise.

// contextFrom extracts a safe, small piece of detail from an optional error passed
// alongside a toast: an Error/APIError's .message. It deliberately never stringifies an
// arbitrary object — a raw API response body can carry tracker credentials.
function contextFrom(err: unknown): string | undefined {
  return err instanceof Error ? err.message : undefined
}

// shipToServer relays an error/warn toast to POST /api/logs/frontend, fire-and-forget:
// the promise is never awaited and any rejection (network down, endpoint unreachable) is
// swallowed, because a logging failure must never itself toast — that would loop — or
// block the UI.
//
// The typeof guard (rather than calling api.postFrontendLog directly) exists for
// testability: several hook tests (useIndexers.test.tsx, useAppConnections.test.tsx)
// mock "@/lib/api" with a partial object exposing only the methods that test exercises,
// so api.postFrontendLog is undefined there. Calling it unconditionally would throw
// "postFrontendLog is not a function" inside a fire-and-forget path with no test-visible
// stack trace. Guarding degrades that case to a silent no-op instead, matching the "must
// never break the UI" rule this function already has to uphold for its non-test callers.
function shipToServer(level: "error" | "warn", message: string, context?: string): void {
  if (typeof api.postFrontendLog !== "function") return
  void api.postFrontendLog(level, message, context).catch(() => {})
}

// notifyError shows an error toast and relays it to the daemon log. err, when passed, is
// typically the caught Error/APIError — only its .message ships as context.
export function notifyError(message: string, err?: unknown): void {
  toast.error(message)
  shipToServer("error", message, contextFrom(err))
}

// notifyWarn shows a warning toast and relays it to the daemon log.
export function notifyWarn(message: string, err?: unknown): void {
  toast.warning(message)
  shipToServer("warn", message, contextFrom(err))
}

// notifySuccess shows a success toast. UI-only — nothing worth logging on the happy path.
export function notifySuccess(message: string): void {
  toast.success(message)
}

// notifyInfo shows an informational toast. UI-only, like notifySuccess.
export function notifyInfo(message: string): void {
  toast.info(message)
}
