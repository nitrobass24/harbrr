import { useState } from "react"
import { ArrowDown, ArrowUp, ChevronDown, ChevronRight, Download, Magnet } from "lucide-react"
import { Badge } from "@/components/ui/badge"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { indexerHue } from "@/components/indexers/IndexerAvatar"
import { acquisitionLink, SendToClientMenu } from "@/components/search/SendToClientMenu"
import { bestMember, rowKey, type ResultGroup } from "@/components/search/search-group"
import type { DownloadClient } from "@/lib/api"
import { formatSize, relativeTime } from "@/lib/format"
import { isSafeHref } from "@/lib/safe-href"
import { cn } from "@/lib/utils"
import type { SearchRow, Sort, SortKey } from "@/components/search/search-sort"

// Merged, client-side-sorted results, one table row per GROUP (autobrr/harbrr#398): a
// single-source group is today's plain row, a multi-source one collapses to the best
// member with a badge per tracker and expands to every tracker's own grabbable entry.
// Grab links are the API-returned URLs rendered VERBATIM as anchor hrefs — the UI never
// rebuilds or logs them (magnets may carry passkeys by design). Trackers are untrusted,
// so the scheme is allowlisted (http/https for `link`, magnet: for `magnet`) before a
// link is rendered at all; anything else (javascript:, data:, etc.) is dropped silently.
export function SearchResultsTable({ groups, catNames, sort, onSort, clients = [] }: {
  groups: ResultGroup[]
  catNames: Map<number, string>
  sort: Sort
  onSort: (key: SortKey) => void
  clients?: DownloadClient[]
}) {
  return (
    <div className="overflow-hidden rounded-xl border border-border bg-card">
      <Table className="text-[13px]">
        <TableHeader>
          <TableRow className="text-[11px] uppercase tracking-wider">
            <SortableHead label="Title" k="title" sort={sort} onSort={onSort} className="pl-5" />
            <TableHead>Indexer</TableHead>
            <TableHead>Category</TableHead>
            <SortableHead label="Size" k="size" sort={sort} onSort={onSort} />
            <SortableHead label="Seeds" k="seeders" sort={sort} onSort={onSort} />
            <TableHead>Leech</TableHead>
            <SortableHead label="Age" k="age" sort={sort} onSort={onSort} />
            <TableHead className="pr-5 text-right">Grab</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {groups.map((group) => (
            <GroupRows key={group.key} group={group} sort={sort} catNames={catNames} clients={clients} />
          ))}
        </TableBody>
      </Table>
    </div>
  )
}

// Per-indexer color attribution: the source of a row is identifiable at a glance rather
// than by reading the column, which matters most once one row carries several sources.
export function IndexerBadge({ slug }: { slug: string }) {
  return (
    <span className="inline-flex items-center gap-1.5 whitespace-nowrap text-muted-foreground">
      <span
        aria-hidden="true"
        className="inline-block h-2 w-2 shrink-0 rounded-full"
        style={{ background: `oklch(0.65 0.15 ${indexerHue(slug)})` }}
      />
      {slug}
    </span>
  )
}

function SortableHead({ label, k, sort, onSort, className }: {
  label: string
  k: SortKey
  sort: Sort
  onSort: (key: SortKey) => void
  className?: string
}) {
  const active = sort.key === k
  return (
    <TableHead className={className}>
      <button
        type="button"
        className={cn("flex cursor-pointer items-center gap-1 uppercase tracking-wider transition hover:text-foreground", active && "text-foreground")}
        onClick={() => onSort(k)}
      >
        {label}
        {active && (sort.dir === "asc" ? <ArrowUp className="h-3 w-3" /> : <ArrowDown className="h-3 w-3" />)}
      </button>
    </TableHead>
  )
}

// A single-source group is indistinguishable from the ungrouped row, so grouping off
// (every group a singleton) renders exactly the flat list.
function GroupRows({ group, sort, catNames, clients }: {
  group: ResultGroup
  sort: Sort
  catNames: Map<number, string>
  clients: DownloadClient[]
}) {
  const [open, setOpen] = useState(false)

  if (group.members.length === 1) {
    return <ResultRow row={group.members[0]} catNames={catNames} clients={clients} />
  }

  const rep = bestMember(group, sort)
  const r = rep.release

  return (
    <>
      <TableRow>
        <TableCell className="max-w-md py-2.5 pl-5">
          <span className="flex items-center gap-2">
            <button
              type="button"
              aria-expanded={open}
              aria-label={`${open ? "Collapse" : "Expand"} ${r.title} — ${group.members.length} sources`}
              className="grid h-5 w-5 shrink-0 place-items-center rounded text-muted-foreground transition hover:bg-accent hover:text-foreground"
              onClick={() => setOpen((v) => !v)}
            >
              {open ? <ChevronDown className="h-3.5 w-3.5" /> : <ChevronRight className="h-3.5 w-3.5" />}
            </button>
            <span className="truncate font-medium" title={r.title}>{r.title}</span>
            {r.downloadVolumeFactor === 0 && <FreeleechBadge />}
          </span>
        </TableCell>
        <TableCell>
          <span className="flex flex-wrap items-center gap-x-2 gap-y-1">
            {group.members.map((m) => <IndexerBadge key={rowKey(m)} slug={m.indexer} />)}
            <span className="text-faint">({group.members.length})</span>
          </span>
        </TableCell>
        <TableCell className="text-muted-foreground">{categoryName(rep, catNames)}</TableCell>
        <TableCell className="whitespace-nowrap">{formatSize(r.size)}</TableCell>
        <TableCell className={cn((r.seeders ?? 0) > 0 ? "text-ok" : "text-faint")}>{r.seeders ?? 0}</TableCell>
        <TableCell className="text-faint">{r.leechers ?? 0}</TableCell>
        <TableCell className="whitespace-nowrap text-muted-foreground">
          {r.publishDate ? relativeTime(r.publishDate) : "—"}
        </TableCell>
        {/* Grabbing is per tracker — which one matters — so the action lives on the
            expanded per-tracker rows, never on the collapsed summary. */}
        <TableCell className="pr-5" />
      </TableRow>
      {open && group.members.map((m) => (
        <ResultRow key={rowKey(m)} row={m} catNames={catNames} clients={clients} nested />
      ))}
    </>
  )
}

function FreeleechBadge() {
  return <Badge className="shrink-0 border-ok/40 bg-ok/10 px-1.5 py-0 text-[10px] text-ok" variant="outline">FL</Badge>
}

// First category id that the union of capability trees names, else the raw id.
export function categoryName(row: SearchRow, catNames: Map<number, string>): string {
  const r = row.release
  return (r.categories ?? [])
    .map((id) => catNames.get(id))
    .find((name) => name !== undefined) ?? (r.categories?.[0] !== undefined ? String(r.categories[0]) : "")
}

function ResultRow({ row, catNames, clients, nested }: {
  row: SearchRow
  catNames: Map<number, string>
  clients: DownloadClient[]
  nested?: boolean
}) {
  const r = row.release
  const freeleech = r.downloadVolumeFactor === 0
  // Inside an expanded group the titles are the same release, so the tracker is what
  // tells the actions apart for a screen reader.
  const from = nested ? ` from ${row.indexer}` : ""

  return (
    <TableRow className={cn(nested && "bg-muted/30")}>
      <TableCell className={cn("max-w-md py-2.5", nested ? "pl-12" : "pl-5")}>
        <span className="flex items-center gap-2">
          <span className={cn("truncate", nested ? "text-muted-foreground" : "font-medium")} title={r.title}>{r.title}</span>
          {freeleech && <FreeleechBadge />}
        </span>
      </TableCell>
      <TableCell><IndexerBadge slug={row.indexer} /></TableCell>
      <TableCell className="text-muted-foreground">{categoryName(row, catNames)}</TableCell>
      <TableCell className="whitespace-nowrap">{formatSize(r.size)}</TableCell>
      <TableCell className={cn((r.seeders ?? 0) > 0 ? "text-ok" : "text-faint")}>{r.seeders ?? 0}</TableCell>
      <TableCell className="text-faint">{r.leechers ?? 0}</TableCell>
      <TableCell className="whitespace-nowrap text-muted-foreground">
        {r.publishDate ? relativeTime(r.publishDate) : "—"}
      </TableCell>
      <TableCell className="pr-5">
        <span className="flex items-center justify-end gap-1">
          {r.link && isSafeHref(r.link, ["http:", "https:"]) && (
            <a
              href={r.link}
              target="_blank"
              rel="noopener noreferrer"
              aria-label={`Download ${r.title}${from}`}
              className="grid h-7 w-7 place-items-center rounded-md text-muted-foreground transition hover:bg-accent hover:text-foreground"
            >
              <Download className="h-4 w-4" />
            </a>
          )}
          {r.magnet && isSafeHref(r.magnet, ["magnet:"]) && (
            <a
              href={r.magnet}
              target="_blank"
              rel="noopener noreferrer"
              aria-label={`Magnet for ${r.title}${from}`}
              className="grid h-7 w-7 place-items-center rounded-md text-muted-foreground transition hover:bg-accent hover:text-foreground"
            >
              <Magnet className="h-4 w-4" />
            </a>
          )}
          <SendToClientMenu
            clients={clients}
            indexer={row.indexer}
            link={acquisitionLink(r)}
            title={r.title}
            source={nested ? row.indexer : undefined}
            className="h-7 w-7"
          />
        </span>
      </TableCell>
    </TableRow>
  )
}
