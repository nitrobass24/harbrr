import { ArrowRight, Copy, MoreVertical, Pencil, Trash2 } from "lucide-react"
import { FreeleechPill } from "@/components/indexers/FreeleechPill"
import { HealthCell } from "@/components/indexers/HealthCell"
import { IndexerAvatar } from "@/components/indexers/IndexerAvatar"
import { ProtocolPill } from "@/components/indexers/ProtocolPill"
import { TypePill } from "@/components/indexers/TypePill"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger
} from "@/components/ui/dropdown-menu"
import { Switch } from "@/components/ui/switch"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { hostname } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { Instance, IndexerStatus } from "@/lib/api"

export type IndexerRowData = {
  instance: Instance
  type?: string // privacy: private | public | semi-private (from definition)
  categories?: string // parent category names, joined
  status?: IndexerStatus
  testing?: boolean
}

export type IndexerRowActions = {
  onToggle: (slug: string, enabled: boolean) => void
  onTest: (slug: string) => void
  onEdit: (slug: string) => void
  onDelete: (slug: string) => void
  onSnippet: (slug: string) => void
  onCopyFeedUrl: (slug: string) => void
  onDetails: (slug: string) => void
}

// Presentational indexers table (mockup layout); the route composes the row
// data from queries so tests can feed fixtures directly.
export function IndexersTable({ rows, actions }: { rows: IndexerRowData[], actions: IndexerRowActions }) {
  return (
    <div className="overflow-hidden rounded-xl border border-border bg-card">
      <Table className="text-[13px]">
        <TableHeader>
          <TableRow className="text-[11px] uppercase tracking-wider">
            <TableHead className="pl-5">Indexer</TableHead>
            <TableHead>Protocol</TableHead>
            <TableHead>Privacy</TableHead>
            <TableHead>Categories</TableHead>
            <TableHead>Health</TableHead>
            <TableHead className="text-center">Enabled</TableHead>
            <TableHead className="pr-5 text-right">Actions</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.map((row) => <IndexerRow key={row.instance.slug} row={row} actions={actions} />)}
        </TableBody>
      </Table>
    </div>
  )
}

function IndexerRow({ row, actions }: { row: IndexerRowData, actions: IndexerRowActions }) {
  const ix = row.instance

  return (
    <TableRow data-slug={ix.slug}>
      <TableCell className="py-3 pl-5">
        <button
          type="button"
          className="flex cursor-pointer items-center gap-3 text-left"
          onClick={() => actions.onDetails(ix.slug)}
        >
          <IndexerAvatar slug={ix.slug} name={ix.name} />
          <span className="flex flex-col leading-tight">
            <span className={cn("font-medium", ix.enabled ? "text-foreground" : "text-muted-foreground")}>
              {ix.name}
            </span>
            <span className="text-[12px] text-faint">{hostname(ix.baseUrl) || ix.definitionId}</span>
          </span>
        </button>
      </TableCell>
      <TableCell><ProtocolPill protocol={row.instance.protocol} /></TableCell>
      <TableCell>
        <div className="flex items-center gap-1.5">
          <TypePill type={row.type} />
          <FreeleechPill freeleech={ix.freeleech} />
        </div>
      </TableCell>
      <TableCell className="max-w-56 truncate text-muted-foreground">{row.categories ?? ""}</TableCell>
      <TableCell><HealthCell status={row.status} /></TableCell>
      <TableCell className="text-center">
        <Switch
          aria-label={`${ix.enabled ? "Disable" : "Enable"} ${ix.name}`}
          checked={ix.enabled}
          onCheckedChange={(checked) => actions.onToggle(ix.slug, checked)}
        />
      </TableCell>
      <TableCell className="pr-5">
        <div className="flex items-center justify-end gap-1">
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
      </TableCell>
    </TableRow>
  )
}
