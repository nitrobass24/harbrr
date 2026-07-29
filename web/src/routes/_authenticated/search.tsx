import { useDeferredValue, useEffect, useMemo, useRef, useState } from "react"
import { createFileRoute } from "@tanstack/react-router"
import { ChevronDown, Filter, Search as SearchIcon, X } from "lucide-react"
import { PageHeader } from "@/components/layout/PageHeader"
import { filterRows } from "@/components/search/search-filter"
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
import { useSearchAggregate } from "@/hooks/useSearch"
import type { SearchMember, SearchParams } from "@/lib/api"

export const Route = createFileRoute("/_authenticated/search")({
  component: SearchPage,
})

// The window the server is asked for. It clamps to its own advertised maximum, so this
// is a ceiling, not a promise — `total` says how big the merged set behind it was.
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
  const [submitted, setSubmitted] = useState<SearchParams | null>(null)
  // "age asc" is newest-first — the order the server merged in — so an untouched table
  // renders the server's window as served, and a column click takes over from there.
  const [sort, setSort] = useState<Sort>({ key: "age", dir: "asc" })

  const results = useSearchAggregate(active, submitted)

  // The whole window is in state, so sorting a column covers everything the server
  // served — there is no second page hiding rows from the sort (autobrr/harbrr#396).
  const rows: SearchRow[] = useMemo(
    () => sortRows((results.data?.results ?? []).map((r) => ({ release: r.release, indexer: r.indexer })), sort),
    [results.data, sort]
  )

  // Client-side only: filtering narrows the rows already in state (never the DOM,
  // never a re-query). useDeferredValue keeps typing responsive when a heavy regex
  // meets PAGE_SIZE x N-indexers rows, without a debounce timer to manage.
  const [filter, setFilter] = useState("")
  const deferredFilter = useDeferredValue(filter)
  const lastValidFilter = useRef("")
  const { shown, invalidFilter } = useMemo(() => {
    const matched = filterRows(rows, deferredFilter, catNames)
    if (matched !== null) {
      return { shown: matched, invalidFilter: false }
    }
    // Half-typed or uncompilable pattern: re-apply the last valid one to the current
    // rows so the view stays put instead of blanking.
    return { shown: filterRows(rows, lastValidFilter.current, catNames) ?? rows, invalidFilter: true }
  }, [rows, deferredFilter, catNames])
  // The last-valid ledger updates POST-COMMIT, never during render: an abandoned
  // deferred pass must not be able to write a filter string no committed view showed
  // (review finding — render-phase ref mutation is unsafe under concurrent rendering).
  useEffect(() => {
    if (!invalidFilter) lastValidFilter.current = deferredFilter
  }, [invalidFilter, deferredFilter])

  // Mirrors useSearchAggregate's enabled condition: a DISABLED query stays `pending`
  // forever, so without the subset check an empty indexer selection would render
  // "Searching 0 indexers…" indefinitely and hide the results/ledger (review finding).
  const searching = submitted !== null && active.length > 0 && results.isPending
  const total = results.data?.total ?? rows.length

  const search = () => {
    setSubmitted({
      q: q || undefined,
      cat: cat || undefined,
      imdbid: ids.imdbid || undefined,
      tmdbid: ids.tmdbid || undefined,
      tvdbid: ids.tvdbid || undefined,
      season: ids.season || undefined,
      ep: ids.ep || undefined,
      limit: PAGE_SIZE,
    })
  }

  const setId = (key: keyof typeof ids) => (value: string) => setIds((prev) => ({ ...prev, [key]: value }))

  const countLabel = shown.length === rows.length ? `${rows.length} results` : `${shown.length} of ${rows.length} results`

  return (
    <div className="flex h-full flex-col">
      <PageHeader title="Search" subtitle={`Manual search across ${active.length} of ${enabled.length} enabled indexers`} />

      <div className="min-h-0 flex-1 overflow-auto px-4 md:px-7 py-6">
        <form
          className="mb-5 flex flex-col gap-3"
          onSubmit={(e) => {
            e.preventDefault()
            search()
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

        {searching && (
          <p className="mb-3 px-1 text-[12px] text-faint">Searching {active.length} {active.length === 1 ? "indexer" : "indexers"}…</p>
        )}

        {results.isError && (
          <p className="mb-3 rounded-md border border-warn/40 bg-warn/10 px-3 py-2 text-[13px] text-warn">
            Search failed — {results.error.message}
          </p>
        )}

        {submitted !== null && !searching && results.data && (
          <>
            <SearchLedger members={results.data.members} />
            {rows.length > 0 ? (
              <>
                <ResultFilter value={filter} onChange={setFilter} invalid={invalidFilter} />
                {shown.length > 0 ? (
                  <SearchResultsResponsive rows={shown} catNames={catNames} sort={sort} onSort={(key: SortKey) =>
                    setSort((prev) => prev.key === key ? { key, dir: prev.dir === "desc" ? "asc" : "desc" } : { key, dir: "desc" })} />
                ) : (
                  <div className="grid place-items-center rounded-xl border border-dashed border-border py-16 text-center">
                    <p className="text-[13px] text-muted-foreground">No results match the filter.</p>
                  </div>
                )}
                <div className="mt-3 flex items-center gap-3 px-1 text-[12px] text-faint">
                  <span>{countLabel}</span>
                  {/* The server merged and windowed this set, so the count it stands behind
                      is stated whenever it is larger than what fits in one window. */}
                  {total > rows.length && <span>· newest {rows.length} of {total} fetched</span>}
                </div>
              </>
            ) : (
              <div className="grid place-items-center rounded-xl border border-dashed border-border py-16 text-center">
                <p className="text-[13px] text-muted-foreground">No results.</p>
              </div>
            )}
          </>
        )}
      </div>
    </div>
  )
}

// The closed skip vocabulary the server serves (core's Skip* constants), in operator
// words. An unmapped value renders raw — the vocabulary is closed, so it is readable
// as-is and a new one must never render as a blank.
const SKIP_LABEL: Record<string, string> = {
  "budget-exhausted": "budget exhausted",
  "circuit-open": "circuit open",
  "degenerate-query": "nothing left of the query to search",
  "rate-limited": "rate limited",
  unreachable: "unreachable",
  timeout: "timed out",
  error: "failed",
  disabled: "disabled",
  unavailable: "unavailable",
}

// The per-member ledger behind the merged window: answering indexers fold into one
// line, and each skipped one names itself and why it contributed nothing. Skips are
// information, not alarms — they get the flat, quiet treatment (the tone an unknown
// health state gets), never a warning banner.
function SearchLedger({ members }: { members: SearchMember[] }) {
  const ok = members.filter((m) => m.status === "ok")
  const skipped = members.filter((m) => m.status !== "ok")
  const answered = ok.reduce((n, m) => n + m.count, 0)

  return (
    <div className="mb-3 flex flex-wrap items-center gap-x-3 gap-y-1 px-1 text-[12px] text-faint">
      <span>{ok.length} {ok.length === 1 ? "indexer" : "indexers"} · {answered} results</span>
      {skipped.map((m) => (
        <span key={m.slug} className="flex items-center gap-1.5">
          <span className="inline-flex h-1.5 w-1.5 rounded-full bg-faint" />
          {m.name} — {m.reason ? SKIP_LABEL[m.reason] ?? m.reason : "no answer"}
        </span>
      ))}
    </div>
  )
}

// Narrows the results already on screen. Never re-queries — this is a view over
// state the server already returned.
function ResultFilter({ value, onChange, invalid }: {
  value: string
  onChange: (v: string) => void
  invalid: boolean
}) {
  return (
    <div className="mb-3 flex items-center gap-2">
      <div className="relative w-full max-w-md">
        <Filter className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-faint" />
        <Input
          className="h-8 pl-8 pr-8 text-[13px]"
          aria-label="Filter results"
          aria-invalid={invalid}
          placeholder="Filter results — text, /regex/, -exclude"
          value={value}
          onChange={(e) => onChange(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Escape") onChange("")
          }}
        />
        {value !== "" && (
          <button
            type="button"
            aria-label="Clear filter"
            className="absolute right-2 top-1/2 -translate-y-1/2 text-faint hover:text-foreground"
            onClick={() => onChange("")}
          >
            <X className="h-3.5 w-3.5" />
          </button>
        )}
      </div>
      {invalid && <span className="text-[12px] text-warn">Invalid pattern — showing the last valid filter</span>}
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
