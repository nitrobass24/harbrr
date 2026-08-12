// A deterministic OKLCH hue from the slug, so an indexer keeps one stable color across
// every surface that attributes a result to it, without any stored asset.
export function indexerHue(slug: string): number {
  let hash = 0
  for (const ch of slug) hash = (hash * 31 + ch.codePointAt(0)!) >>> 0
  return hash % 360
}

// Colored-initial avatar (mockup pattern).
export function IndexerAvatar({ slug, name }: { slug: string, name: string }) {
  const hue = indexerHue(slug)

  return (
    <div
      className="grid h-8 w-8 shrink-0 place-items-center rounded-md text-[13px] font-bold text-white"
      style={{ background: `oklch(0.55 0.15 ${hue})` }}
    >
      {(name[0] ?? "?").toUpperCase()}
    </div>
  )
}
