/*
 * Copyright (c) 2025-2026, s0up and the autobrr contributors.
 * SPDX-License-Identifier: GPL-2.0-or-later
 */

import { Download, Magnet } from "lucide-react"
import { Badge } from "@/components/ui/badge"
import { formatSize, relativeTime } from "@/lib/format"
import { isSafeHref } from "@/lib/safe-href"
import { cn } from "@/lib/utils"
import type { SearchRow } from "@/components/search/search-sort"

// Mobile card list, mirroring SearchResultsTable's ResultRow content (title, indexer,
// category, size/seeders/leechers, Grab) in a stacked card instead of table columns.
export function SearchResultCardsMobile({ rows, catNames }: {
  rows: SearchRow[]
  catNames: Map<number, string>
}) {
  return (
    <div className="flex flex-col gap-2">
      {rows.map((row) => (
        <ResultCard
          key={`${row.indexer}::${row.release.link ?? row.release.magnet ?? row.release.infohash ?? row.release.title}`}
          row={row}
          catNames={catNames}
        />
      ))}
    </div>
  )
}

function ResultCard({ row, catNames }: { row: SearchRow, catNames: Map<number, string> }) {
  const r = row.release
  const freeleech = r.downloadVolumeFactor === 0
  const category = (r.categories ?? [])
    .map((id) => catNames.get(id))
    .find((name) => name !== undefined) ?? (r.categories?.[0] !== undefined ? String(r.categories[0]) : "")

  return (
    <div className="rounded-lg border border-border bg-card p-3">
      <div className="mb-2 flex items-start justify-between gap-2">
        <h3 className="line-clamp-2 break-all text-[13px] font-medium" title={r.title}>{r.title}</h3>
        {freeleech && <Badge className="shrink-0 border-ok/40 bg-ok/10 px-1.5 py-0 text-[10px] text-ok" variant="outline">FL</Badge>}
      </div>

      <div className="mb-2 flex flex-wrap items-center gap-x-2 gap-y-1 text-[12px] text-muted-foreground">
        <span>{row.indexer}</span>
        {category && (
          <>
            <span aria-hidden="true">·</span>
            <span>{category}</span>
          </>
        )}
      </div>

      <div className="flex items-center justify-between gap-2">
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[12px]">
          <span className="text-muted-foreground">{formatSize(r.size)}</span>
          <span className={cn((r.seeders ?? 0) > 0 ? "text-ok" : "text-faint")}>{r.seeders ?? 0} seeds</span>
          <span className="text-faint">{r.leechers ?? 0} leech</span>
          {r.publishDate && <span className="text-muted-foreground">{relativeTime(r.publishDate)}</span>}
        </div>
        <span className="flex shrink-0 items-center gap-1">
          {r.link && isSafeHref(r.link, ["http:", "https:"]) && (
            <a
              href={r.link}
              target="_blank"
              rel="noopener noreferrer"
              aria-label={`Download ${r.title}`}
              className="grid h-8 w-8 place-items-center rounded-md text-muted-foreground transition hover:bg-accent hover:text-foreground"
            >
              <Download className="h-4 w-4" />
            </a>
          )}
          {r.magnet && isSafeHref(r.magnet, ["magnet:"]) && (
            <a
              href={r.magnet}
              target="_blank"
              rel="noopener noreferrer"
              aria-label={`Magnet for ${r.title}`}
              className="grid h-8 w-8 place-items-center rounded-md text-muted-foreground transition hover:bg-accent hover:text-foreground"
            >
              <Magnet className="h-4 w-4" />
            </a>
          )}
        </span>
      </div>
    </div>
  )
}
