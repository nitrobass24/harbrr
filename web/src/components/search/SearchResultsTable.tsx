import { ArrowDown, ArrowUp, Download, Magnet } from "lucide-react"
import { Badge } from "@/components/ui/badge"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { formatSize, relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { SearchRow, Sort, SortKey } from "@/components/search/search-sort"

// Merged, client-side-sorted results. Grab links are the API-returned URLs
// rendered VERBATIM as anchor hrefs — the UI never rebuilds or logs them
// (magnets may carry passkeys by design).
export function SearchResultsTable({ rows, catNames, sort, onSort }: {
  rows: SearchRow[]
  catNames: Map<number, string>
  sort: Sort
  onSort: (key: SortKey) => void
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
          {rows.map((row, i) => <ResultRow key={`${row.indexer}-${i}`} row={row} catNames={catNames} />)}
        </TableBody>
      </Table>
    </div>
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

function ResultRow({ row, catNames }: { row: SearchRow, catNames: Map<number, string> }) {
  const r = row.release
  const freeleech = r.downloadVolumeFactor === 0
  const category = (r.categories ?? [])
    .map((id) => catNames.get(id))
    .find((name) => name !== undefined) ?? (r.categories?.[0] !== undefined ? String(r.categories[0]) : "")

  return (
    <TableRow>
      <TableCell className="max-w-md py-2.5 pl-5">
        <span className="flex items-center gap-2">
          <span className="truncate font-medium" title={r.title}>{r.title}</span>
          {freeleech && <Badge className="shrink-0 border-ok/40 bg-ok/10 px-1.5 py-0 text-[10px] text-ok" variant="outline">FL</Badge>}
        </span>
      </TableCell>
      <TableCell className="text-muted-foreground">{row.indexer}</TableCell>
      <TableCell className="text-muted-foreground">{category}</TableCell>
      <TableCell className="whitespace-nowrap">{formatSize(r.size)}</TableCell>
      <TableCell className={cn((r.seeders ?? 0) > 0 ? "text-ok" : "text-faint")}>{r.seeders ?? 0}</TableCell>
      <TableCell className="text-faint">{r.leechers ?? 0}</TableCell>
      <TableCell className="whitespace-nowrap text-muted-foreground">
        {r.publishDate ? relativeTime(r.publishDate) : "—"}
      </TableCell>
      <TableCell className="pr-5">
        <span className="flex items-center justify-end gap-1">
          {r.link && (
            <a
              href={r.link}
              aria-label={`Download ${r.title}`}
              className="grid h-7 w-7 place-items-center rounded-md text-muted-foreground transition hover:bg-accent hover:text-foreground"
            >
              <Download className="h-4 w-4" />
            </a>
          )}
          {r.magnet && (
            <a
              href={r.magnet}
              aria-label={`Magnet for ${r.title}`}
              className="grid h-7 w-7 place-items-center rounded-md text-muted-foreground transition hover:bg-accent hover:text-foreground"
            >
              <Magnet className="h-4 w-4" />
            </a>
          )}
        </span>
      </TableCell>
    </TableRow>
  )
}
