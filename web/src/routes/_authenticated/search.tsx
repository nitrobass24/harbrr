import { useMemo, useState } from "react"
import { createFileRoute } from "@tanstack/react-router"
import { ChevronDown, Search as SearchIcon } from "lucide-react"
import { PageHeader } from "@/components/layout/PageHeader"
import { sortRows, type SearchRow, type Sort, type SortKey } from "@/components/search/search-sort"
import { SearchResultsResponsive } from "@/components/search/SearchResultsResponsive"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuSeparator,
  DropdownMenuTrigger
} from "@/components/ui/dropdown-menu"
import { Input } from "@/components/ui/input"
import { NativeSelect } from "@/components/ui/native-select"
import { useIndexerCapabilitiesMany, useIndexers } from "@/hooks/useIndexers"
import { useSearchFanout } from "@/hooks/useSearch"
import type { SearchParams } from "@/lib/api"

export const Route = createFileRoute("/_authenticated/search")({
  component: SearchPage,
})

const PAGE_SIZE = 100

function SearchPage() {
  const indexers = useIndexers()
  const enabled = useMemo(() => (indexers.data ?? []).filter((ix) => ix.enabled), [indexers.data])
  const [selected, setSelected] = useState<Set<string> | null>(null) // null = all enabled
  const slugs = enabled.map((ix) => ix.slug)
  const active = selected === null ? slugs : slugs.filter((s) => selected.has(s))

  const caps = useIndexerCapabilitiesMany(active)

  // Union of the selected indexers' capability trees drives the pickers.
  const { catNames, parentCats, modeParams } = useMemo(() => {
    const catNames = new Map<number, string>()
    const parentCats = new Map<number, string>()
    const modeParams = new Set<string>()
    for (const c of caps) {
      for (const cat of c.data?.categories ?? []) {
        if (!catNames.has(cat.id)) catNames.set(cat.id, cat.name)
        if ((cat.isParent || !cat.parent) && !parentCats.has(cat.id)) parentCats.set(cat.id, cat.name)
      }
      for (const params of Object.values(c.data?.modes ?? {})) {
        for (const p of params) modeParams.add(p)
      }
    }
    return { catNames, parentCats, modeParams }
  }, [caps])

  const [q, setQ] = useState("")
  const [cat, setCat] = useState("")
  const [ids, setIds] = useState({ imdbid: "", tmdbid: "", tvdbid: "", season: "", ep: "" })
  const [offset, setOffset] = useState(0)
  const [submitted, setSubmitted] = useState<SearchParams | null>(null)
  const [sort, setSort] = useState<Sort>({ key: "seeders", dir: "desc" })

  const results = useSearchFanout(active, submitted)

  const rows: SearchRow[] = useMemo(() => {
    const merged: SearchRow[] = []
    results.forEach((res, i) => {
      for (const release of res.data?.results ?? []) {
        merged.push({ release, indexer: active[i] })
      }
    })
    return sortRows(merged, sort)
  }, [results, active, sort])

  const failed = results.map((r, i) => (r.isError ? active[i] : null)).filter((s): s is string => s !== null)
  const searching = submitted !== null && results.some((r) => r.isLoading)
  const hasMore = results.some((r) => r.data?.hasMore)

  const search = (nextOffset: number) => {
    setOffset(nextOffset)
    setSubmitted({
      q: q || undefined,
      cat: cat || undefined,
      imdbid: ids.imdbid || undefined,
      tmdbid: ids.tmdbid || undefined,
      tvdbid: ids.tvdbid || undefined,
      season: ids.season || undefined,
      ep: ids.ep || undefined,
      limit: PAGE_SIZE,
      offset: nextOffset,
    })
  }

  const setId = (key: keyof typeof ids) => (value: string) => setIds((prev) => ({ ...prev, [key]: value }))

  return (
    <div className="flex h-full flex-col">
      <PageHeader title="Search" subtitle={`Manual search across ${active.length} of ${enabled.length} enabled indexers`} />

      <div className="min-h-0 flex-1 overflow-auto px-4 md:px-7 py-6">
        <form
          className="mb-5 flex flex-col gap-3"
          onSubmit={(e) => {
            e.preventDefault()
            search(0)
          }}
        >
          <div className="flex flex-wrap items-end gap-2.5">
            <div className="relative w-full flex-1 md:min-w-64">
              <SearchIcon className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-faint" />
              <Input
                className="h-9 pl-8"
                placeholder="Search query"
                autoFocus
                value={q}
                onChange={(e) => setQ(e.target.value)}
              />
            </div>
            <NativeSelect
              aria-label="Category"
              className="h-9 w-44"
              value={cat}
              onChange={(e) => setCat(e.target.value)}
            >
              <option value="">All categories</option>
              {[...parentCats.entries()].sort((a, b) => a[0] - b[0]).map(([id, name]) => (
                <option key={id} value={String(id)}>{name}</option>
              ))}
            </NativeSelect>
            <Button type="submit" disabled={active.length === 0 || searching}>
              {searching ? "Searching…" : "Search"}
            </Button>
          </div>

          {(modeParams.has("imdbid") || modeParams.has("tmdbid") || modeParams.has("tvdbid") || modeParams.has("season")) && (
            <div className="flex flex-wrap items-center gap-2.5 text-[13px]">
              {modeParams.has("imdbid") && <IdInput label="IMDb ID" value={ids.imdbid} onChange={setId("imdbid")} placeholder="tt0133093" />}
              {modeParams.has("tmdbid") && <IdInput label="TMDB ID" value={ids.tmdbid} onChange={setId("tmdbid")} />}
              {modeParams.has("tvdbid") && <IdInput label="TVDB ID" value={ids.tvdbid} onChange={setId("tvdbid")} />}
              {modeParams.has("season") && <IdInput label="Season" value={ids.season} onChange={setId("season")} narrow />}
              {modeParams.has("ep") && <IdInput label="Episode" value={ids.ep} onChange={setId("ep")} narrow />}
            </div>
          )}

          <IndexerPicker
            slugs={slugs}
            selected={selected}
            onChange={setSelected}
          />
        </form>

        {failed.length > 0 && (
          <p className="mb-3 rounded-md border border-warn/40 bg-warn/10 px-3 py-2 text-[13px] text-warn">
            No response from: {failed.join(", ")}
          </p>
        )}

        {submitted !== null && !searching && (
          rows.length > 0 ? (
            <>
              <SearchResultsResponsive rows={rows} catNames={catNames} sort={sort} onSort={(key: SortKey) =>
                setSort((prev) => prev.key === key ? { key, dir: prev.dir === "desc" ? "asc" : "desc" } : { key, dir: "desc" })} />
              <div className="mt-3 flex items-center gap-3 px-1 text-[12px] text-faint">
                <span>{rows.length} results · page {offset / PAGE_SIZE + 1}</span>
                <span className="ml-auto flex gap-2">
                  {offset > 0 && (
                    <Button variant="outline" size="sm" onClick={() => search(offset - PAGE_SIZE)}>Previous</Button>
                  )}
                  {hasMore && (
                    <Button variant="outline" size="sm" onClick={() => search(offset + PAGE_SIZE)}>Next</Button>
                  )}
                </span>
              </div>
            </>
          ) : (
            <div className="grid place-items-center rounded-xl border border-dashed border-border py-16 text-center">
              <p className="text-[13px] text-muted-foreground">No results.</p>
            </div>
          )
        )}
      </div>
    </div>
  )
}

function IdInput({ label, value, onChange, placeholder, narrow }: {
  label: string
  value: string
  onChange: (v: string) => void
  placeholder?: string
  narrow?: boolean
}) {
  return (
    <label className="flex items-center gap-1.5 text-muted-foreground">
      {label}
      <Input
        className={narrow ? "h-8 w-16" : "h-8 w-28"}
        value={value}
        placeholder={placeholder}
        onChange={(e) => onChange(e.target.value)}
      />
    </label>
  )
}

// Which enabled indexers the fan-out hits; null selection = all of them.
function IndexerPicker({ slugs, selected, onChange }: {
  slugs: string[]
  selected: Set<string> | null
  onChange: (next: Set<string> | null) => void
}) {
  const [filter, setFilter] = useState("")

  if (slugs.length === 0) {
    return <p className="text-[13px] text-muted-foreground">No enabled indexers — add one first.</p>
  }

  const isChecked = (slug: string) => selected === null || selected.has(slug)
  const filtered = filter ? slugs.filter((s) => s.toLowerCase().includes(filter.toLowerCase())) : slugs
  let label = "All indexers"
  if (selected !== null) {
    label = selected.size === 1 ? "1 indexer" : `${selected.size} of ${slugs.length} indexers`
  }

  const toggle = (slug: string, checked: boolean) => {
    const next = new Set(selected ?? slugs)
    if (checked) next.add(slug)
    else next.delete(slug)
    onChange(next.size === slugs.length ? null : next)
  }

  return (
    <div className="flex items-center gap-3 text-[13px]">
      <span className="text-[11px] font-medium uppercase tracking-wider text-faint">Indexers</span>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button variant="outline" size="sm" className="h-8 gap-1.5">
            {label}
            <ChevronDown className="size-3.5" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start" className="w-56">
          <div className="px-2 pb-1 pt-1">
            <Input
              className="h-7 text-[12px]"
              placeholder="Filter indexers…"
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
              onKeyDown={(e) => e.stopPropagation()}
            />
          </div>
          <DropdownMenuSeparator />
          {filter === "" && (
            <>
              <DropdownMenuCheckboxItem
                checked={selected === null}
                onCheckedChange={(checked) => onChange(checked ? null : new Set())}
                onSelect={(e) => e.preventDefault()}
              >
                All
              </DropdownMenuCheckboxItem>
              <DropdownMenuSeparator />
            </>
          )}
          <div className="max-h-52 overflow-y-auto">
            {filtered.map((slug) => (
              <DropdownMenuCheckboxItem
                key={slug}
                checked={isChecked(slug)}
                onCheckedChange={(checked) => toggle(slug, checked === true)}
                onSelect={(e) => e.preventDefault()}
              >
                {slug}
              </DropdownMenuCheckboxItem>
            ))}
            {filtered.length === 0 && (
              <p className="px-2 py-2 text-[12px] text-muted-foreground">No match.</p>
            )}
          </div>
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  )
}
