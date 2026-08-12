import type { ResultGroup } from "@/components/search/search-group"
import type { SearchRow } from "@/components/search/search-sort"

// One filter term: a case-insensitive substring or a /regex/, optionally negated.
type Term = { negate: boolean, match: (hay: string) => boolean }

// Terms are whitespace-separated, except a /regex/ term, which keeps its spaces
// (and may escape a slash as \/).
const TOKEN = /[-!]?\/(?:\\.|[^/\\])*\/|\S+/g

// What a row is matched against: everything the operator can see in a result row
// except the numbers — title, the indexer that returned it, and its category names.
function haystack(row: SearchRow, catNames: Map<number, string>): string {
  const cats = (row.release.categories ?? []).map((id) => catNames.get(id) ?? String(id))
  return [row.release.title, row.indexer, ...cats].join(" ").toLowerCase()
}

// null = not a usable term (unterminated or uncompilable regex).
function parseTerm(token: string): Term | null {
  const negate = token.startsWith("-") || token.startsWith("!")
  const body = negate ? token.slice(1) : token
  // A lone "-" or "!" is mid-typing, not a filter.
  if (body === "") return { negate: false, match: () => true }
  if (!body.startsWith("/")) {
    const needle = body.toLowerCase()
    return { negate, match: (hay) => hay.includes(needle) }
  }
  if (body.length < 2 || !body.endsWith("/")) return null
  try {
    const re = new RegExp(body.slice(1, -1), "i")
    return { negate, match: (hay) => re.test(hay) }
  } catch {
    return null
  }
}

// null = at least one term is a half-typed or invalid regex; an empty array = no terms.
function parseTerms(input: string): Term[] | null {
  const tokens = input.trim().match(TOKEN)
  if (!tokens) return []

  const terms: Term[] = []
  for (const token of tokens) {
    const term = parseTerm(token)
    if (term === null) return null
    terms.push(term)
  }
  return terms
}

function matches(row: SearchRow, terms: Term[], catNames: Map<number, string>): boolean {
  const hay = haystack(row, catNames)
  return terms.every((t) => t.match(hay) !== t.negate)
}

/**
 * filterRows narrows rows to those matching every term in `input`: case-insensitive
 * substring by default, `/pattern/` for a regex, a leading `-` or `!` to exclude.
 * Empty input returns the `rows` array itself, so memo identity holds.
 *
 * Returns null when any term is a half-typed or invalid regex — callers keep the
 * last valid view instead of blanking the results over a character in flight.
 */
export function filterRows(rows: SearchRow[], input: string, catNames: Map<number, string>): SearchRow[] | null {
  const terms = parseTerms(input)
  if (terms === null) return null
  if (terms.length === 0) return rows

  return rows.filter((row) => matches(row, terms, catNames))
}

/**
 * filterGroups is filterRows over grouped results: a group is kept when ANY of its
 * members matches, so a filter never half-collapses a group into a partial source list
 * (autobrr/harbrr#398). Same null-on-invalid contract as filterRows.
 */
export function filterGroups(groups: ResultGroup[], input: string, catNames: Map<number, string>): ResultGroup[] | null {
  const terms = parseTerms(input)
  if (terms === null) return null
  if (terms.length === 0) return groups

  return groups.filter((group) => group.members.some((row) => matches(row, terms, catNames)))
}
