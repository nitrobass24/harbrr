/*
 * Copyright (c) 2025-2026, s0up and the autobrr contributors.
 * SPDX-License-Identifier: GPL-2.0-or-later
 */

import { useState } from "react"
import { ChevronDown, ChevronRight, Download, Magnet } from "lucide-react"
import { Badge } from "@/components/ui/badge"
import { acquisitionLink, SendToClientMenu } from "@/components/search/SendToClientMenu"
import { bestMember, rowKey, type ResultGroup } from "@/components/search/search-group"
import { categoryName, IndexerBadge } from "@/components/search/SearchResultsTable"
import type { DownloadClient } from "@/lib/api"
import { formatSize, relativeTime } from "@/lib/format"
import { isSafeHref } from "@/lib/safe-href"
import { cn } from "@/lib/utils"
import type { SearchRow, Sort } from "@/components/search/search-sort"

// Mobile card list, mirroring SearchResultsTable's rows (title, indexer, category,
// size/seeders/leechers, Grab) in a stacked card instead of table columns — including
// its grouping: a multi-source card collapses to the best member with a badge per
// tracker and expands to every tracker's own grabbable entry (autobrr/harbrr#398).
export function SearchResultCardsMobile({ groups, catNames, sort, clients = [] }: {
  groups: ResultGroup[]
  catNames: Map<number, string>
  sort: Sort
  clients?: DownloadClient[]
}) {
  return (
    <div className="flex flex-col gap-2">
      {groups.map((group) => (
        <GroupCard key={group.key} group={group} sort={sort} catNames={catNames} clients={clients} />
      ))}
    </div>
  )
}

// A single-source group is indistinguishable from the ungrouped card, so grouping off
// (every group a singleton) renders exactly the flat list.
function GroupCard({ group, sort, catNames, clients }: {
  group: ResultGroup
  sort: Sort
  catNames: Map<number, string>
  clients: DownloadClient[]
}) {
  const [open, setOpen] = useState(false)

  if (group.members.length === 1) {
    return <ResultCard row={group.members[0]} catNames={catNames} clients={clients} />
  }

  const rep = bestMember(group, sort)
  const r = rep.release

  return (
    <div className="rounded-lg border border-border bg-card p-3">
      <CardHeading row={rep} />

      <button
        type="button"
        aria-expanded={open}
        aria-label={`${open ? "Collapse" : "Expand"} ${r.title} — ${group.members.length} sources`}
        className="mb-2 flex w-full flex-wrap items-center gap-x-2 gap-y-1 text-left text-[12px] text-muted-foreground"
        onClick={() => setOpen((v) => !v)}
      >
        {open ? <ChevronDown className="h-3.5 w-3.5 shrink-0" /> : <ChevronRight className="h-3.5 w-3.5 shrink-0" />}
        {group.members.map((m) => <IndexerBadge key={rowKey(m)} slug={m.indexer} />)}
        <span className="text-faint">({group.members.length})</span>
        <CategorySuffix row={rep} catNames={catNames} />
      </button>

      <CardStats row={rep} />

      {/* Grabbing is per tracker — which one matters — so the actions live on the
          expanded per-tracker entries, never on the collapsed summary. Each entry
          carries the SAME facts as a card of its own: a member the operator cannot see
          is freeleech, or newer, is a member they cannot pick on. */}
      {open && (
        <div className="mt-2 flex flex-col gap-3 border-t border-border pt-2">
          {group.members.map((m) => (
            <div key={rowKey(m)} className="flex flex-col gap-1">
              <div className="flex items-center justify-between gap-2">
                <span className="flex flex-wrap items-center gap-x-2 gap-y-1 text-[12px] text-muted-foreground">
                  <IndexerBadge slug={m.indexer} />
                  <CategorySuffix row={m} catNames={catNames} />
                  {m.release.downloadVolumeFactor === 0 && <FreeleechBadge />}
                </span>
                <GrabActions row={m} clients={clients} nested />
              </div>
              <CardStats row={m} />
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

function FreeleechBadge() {
  return <Badge className="shrink-0 border-ok/40 bg-ok/10 px-1.5 py-0 text-[10px] text-ok" variant="outline">FL</Badge>
}

function CardHeading({ row }: { row: SearchRow }) {
  const r = row.release
  return (
    <div className="mb-2 flex items-start justify-between gap-2">
      <h3 className="line-clamp-2 break-all text-[13px] font-medium" title={r.title}>{r.title}</h3>
      {r.downloadVolumeFactor === 0 && <FreeleechBadge />}
    </div>
  )
}

function CategorySuffix({ row, catNames }: { row: SearchRow, catNames: Map<number, string> }) {
  const category = categoryName(row, catNames)
  if (!category) return null
  return (
    <>
      <span aria-hidden="true">·</span>
      <span>{category}</span>
    </>
  )
}

function CardStats({ row }: { row: SearchRow }) {
  const r = row.release
  return (
    <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[12px]">
      <span className="text-muted-foreground">{formatSize(r.size)}</span>
      <span className={cn((r.seeders ?? 0) > 0 ? "text-ok" : "text-faint")}>{r.seeders ?? 0} seeds</span>
      <span className="text-faint">{r.leechers ?? 0} leech</span>
      {r.publishDate && <span className="text-muted-foreground">{relativeTime(r.publishDate)}</span>}
    </div>
  )
}

function GrabActions({ row, clients, nested }: { row: SearchRow, clients: DownloadClient[], nested?: boolean }) {
  const r = row.release
  // Inside an expanded group the titles are the same release, so the tracker is what
  // tells the actions apart for a screen reader.
  const from = nested ? ` from ${row.indexer}` : ""

  return (
    <span className="flex shrink-0 items-center gap-1">
      {r.link && isSafeHref(r.link, ["http:", "https:"]) && (
        <a
          href={r.link}
          target="_blank"
          rel="noopener noreferrer"
          aria-label={`Download ${r.title}${from}`}
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
          aria-label={`Magnet for ${r.title}${from}`}
          className="grid h-8 w-8 place-items-center rounded-md text-muted-foreground transition hover:bg-accent hover:text-foreground"
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
        className="h-8 w-8"
      />
    </span>
  )
}

function ResultCard({ row, catNames, clients }: { row: SearchRow, catNames: Map<number, string>, clients: DownloadClient[] }) {
  return (
    <div className="rounded-lg border border-border bg-card p-3">
      <CardHeading row={row} />

      <div className="mb-2 flex flex-wrap items-center gap-x-2 gap-y-1 text-[12px] text-muted-foreground">
        <IndexerBadge slug={row.indexer} />
        <CategorySuffix row={row} catNames={catNames} />
      </div>

      <div className="flex items-center justify-between gap-2">
        <CardStats row={row} />
        <GrabActions row={row} clients={clients} />
      </div>
    </div>
  )
}
