/*
 * Copyright (c) 2025-2026, s0up and the autobrr contributors.
 * SPDX-License-Identifier: GPL-2.0-or-later
 */

import { useIsMobile } from "@/hooks/useMediaQuery"
import { SearchResultCardsMobile } from "@/components/search/SearchResultCardsMobile"
import { SearchResultsTable } from "@/components/search/SearchResultsTable"
import type { SearchRow, Sort, SortKey } from "@/components/search/search-sort"
import type { DownloadClient } from "@/lib/api"

// Renders the table on md+ and cards on mobile, keyed off useIsMobile — mirroring
// qui's TorrentTableResponsive -> TorrentCardsMobile switch. Same rows/catNames feed
// both, so sorting and the Grab links behave identically either way.
export function SearchResultsResponsive({ rows, catNames, sort, onSort, clients }: {
  rows: SearchRow[]
  catNames: Map<number, string>
  sort: Sort
  onSort: (key: SortKey) => void
  clients?: DownloadClient[]
}) {
  const isMobile = useIsMobile()

  if (isMobile) {
    return <SearchResultCardsMobile rows={rows} catNames={catNames} clients={clients} />
  }

  return <SearchResultsTable rows={rows} catNames={catNames} sort={sort} onSort={onSort} clients={clients} />
}
