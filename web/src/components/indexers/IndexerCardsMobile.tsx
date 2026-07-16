import { ArrowRight, Copy, MoreVertical, Pencil, Trash2 } from "lucide-react"
import { FreeleechPill } from "@/components/indexers/FreeleechPill"
import { HealthCell } from "@/components/indexers/HealthCell"
import { IndexerAvatar } from "@/components/indexers/IndexerAvatar"
import { ProtocolPill } from "@/components/indexers/ProtocolPill"
import { TypePill } from "@/components/indexers/TypePill"
import type { IndexerRowActions, IndexerRowData } from "@/components/indexers/IndexersTable"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger
} from "@/components/ui/dropdown-menu"
import { Switch } from "@/components/ui/switch"
import { hostname } from "@/lib/format"
import { cn } from "@/lib/utils"

// Mobile card layout for the Indexers screen (autobrr/harbrr#94), mirroring
// qui's TorrentCardsMobile → TorrentTableResponsive pattern: same row data +
// actions as IndexersTable, laid out as stacked cards instead of a table.
export function IndexerCardsMobile({ rows, actions }: { rows: IndexerRowData[], actions: IndexerRowActions }) {
  return (
    <div className="flex flex-col gap-3">
      {rows.map((row) => <IndexerCard key={row.instance.slug} row={row} actions={actions} />)}
    </div>
  )
}

function IndexerCard({ row, actions }: { row: IndexerRowData, actions: IndexerRowActions }) {
  const ix = row.instance

  return (
    <div data-slug={ix.slug} className="rounded-xl border border-border bg-card p-4">
      <div className="flex items-start justify-between gap-3">
        <button
          type="button"
          className="flex min-w-0 cursor-pointer items-center gap-3 text-left"
          onClick={() => actions.onDetails(ix.slug)}
        >
          <IndexerAvatar slug={ix.slug} name={ix.name} />
          <span className="flex min-w-0 flex-col leading-tight">
            <span className={cn("truncate font-medium", ix.enabled ? "text-foreground" : "text-muted-foreground")}>
              {ix.name}
            </span>
            <span className="truncate text-[12px] text-faint">{hostname(ix.baseUrl) || ix.definitionId}</span>
          </span>
        </button>
        <Switch
          aria-label={`${ix.enabled ? "Disable" : "Enable"} ${ix.name}`}
          checked={ix.enabled}
          onCheckedChange={(checked) => actions.onToggle(ix.slug, checked)}
        />
      </div>

      <div className="mt-3 flex flex-wrap items-center gap-1.5">
        <ProtocolPill protocol={row.instance.protocol} />
        <TypePill type={row.type} />
        <FreeleechPill freeleech={ix.freeleech} />
      </div>

      {row.categories && (
        <p className="mt-2 truncate text-[13px] text-muted-foreground">{row.categories}</p>
      )}

      <div className="mt-2">
        <HealthCell status={row.status} />
      </div>

      <div className="mt-3 flex items-center justify-end gap-1 border-t border-border pt-3">
        <Button
          variant="outline"
          size="sm"
          disabled={row.testing}
          onClick={() => actions.onTest(ix.slug)}
        >
          {row.testing ? "Testing…" : (
            <>
              <ArrowRight className="h-3.5 w-3.5" />
              Test
            </>
          )}
        </Button>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="ghost" size="icon" aria-label={`More actions for ${ix.name}`}>
              <MoreVertical className="h-4 w-4" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem onClick={() => actions.onEdit(ix.slug)}>
              <Pencil className="h-4 w-4" /> Edit
            </DropdownMenuItem>
            <DropdownMenuItem onClick={() => actions.onCopyFeedUrl(ix.slug)}>
              <Copy className="h-4 w-4" /> Copy feed URL
            </DropdownMenuItem>
            <DropdownMenuItem onClick={() => actions.onSnippet(ix.slug)}>
              <Copy className="h-4 w-4" /> Cross-seed snippet
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem variant="destructive" onClick={() => actions.onDelete(ix.slug)}>
              <Trash2 className="h-4 w-4" /> Delete
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
    </div>
  )
}
