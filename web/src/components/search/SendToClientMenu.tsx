import { Send } from "lucide-react"
import { useState } from "react"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger
} from "@/components/ui/dropdown-menu"
import { api } from "@/lib/api"
import type { DownloadClient, Release } from "@/lib/api"
import { notifyError, notifySuccess } from "@/lib/notify"
import { isSafeHref } from "@/lib/safe-href"
import { cn } from "@/lib/utils"

// acquisitionLink is the value the server sealed and the Grab column renders: the
// release's `link`, falling back to its `magnet`. Trackers are untrusted, so the same
// scheme allowlist the anchors use gates it — a javascript:/data: value is no more
// sendable than it is clickable. undefined means there is nothing to send.
export function acquisitionLink(r: Release): string | undefined {
  const acq = r.link ?? r.magnet
  if (!acq || !isSafeHref(acq, ["http:", "https:", "magnet:"])) return undefined
  return acq
}

// SendToClientMenu is the "send this result to a download client" control shared by
// both search surfaces (autobrr/harbrr#7). It renders nothing when there is no enabled
// client to send to, or no link to send — a resolver-needing indexer's link is withheld
// when the download proxy is off, and there is nothing to hand a client then.
//
// The link is posted back VERBATIM: it is whatever the search response served (a sealed
// harbrr link, a magnet, or a direct URL), and the server classifies it. The UI never
// rebuilds it and never logs it — a link may carry a passkey by design.
export function SendToClientMenu({ clients, indexer, link, title, className }: {
  clients: DownloadClient[]
  indexer: string
  link: string | undefined
  title: string
  className?: string
}) {
  const [sending, setSending] = useState(false)

  if (clients.length === 0 || !link) return null

  const send = async (client: DownloadClient) => {
    setSending(true)
    try {
      await api.grabToDownloadClient(client.id, { indexer, link, name: title })
      notifySuccess(`Sent to ${client.name}`)
    } catch (err) {
      notifyError(`Sending to ${client.name} failed`, err)
    } finally {
      setSending(false)
    }
  }

  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        disabled={sending}
        aria-label={`Send ${title} to a download client`}
        className={cn(
          "grid place-items-center rounded-md text-muted-foreground transition hover:bg-accent hover:text-foreground disabled:opacity-50",
          className
        )}
      >
        <Send className="h-4 w-4" />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        {clients.map((client) => (
          <DropdownMenuItem key={client.id} onSelect={() => void send(client)}>
            {client.name}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
