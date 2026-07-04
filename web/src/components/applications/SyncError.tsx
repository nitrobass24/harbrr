import { Copy } from "lucide-react"
import { copyText } from "@/lib/clipboard"

// SyncError renders a scrubbed app-sync / push error in full: selectable, wrapping,
// height-capped with its own scroll, plus a copy button. The old one-line `truncate`
// + native `title` tooltip hid Servarr's actual validation reason (#57) — now that the
// backend surfaces it, it has to be readable and copyable.
export function SyncError({ error }: { error: string }) {
  return (
    <div className="mt-1 flex w-full items-start gap-1.5 rounded-md border border-bad/30 bg-bad/5 px-2 py-1.5">
      <p className="max-h-32 flex-1 overflow-y-auto whitespace-pre-wrap break-words text-[12px] text-bad select-text">
        {error}
      </p>
      <button
        type="button"
        aria-label="Copy error"
        className="shrink-0 cursor-pointer text-bad/70 transition hover:text-bad"
        onClick={() => void copyText(error, "Error copied")}
      >
        <Copy className="h-3.5 w-3.5" />
      </button>
    </div>
  )
}
