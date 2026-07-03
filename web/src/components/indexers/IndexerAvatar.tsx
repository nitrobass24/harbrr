// Colored-initial avatar: a deterministic OKLCH hue from the slug so every
// indexer keeps a stable color without any stored asset (mockup pattern).
export function IndexerAvatar({ slug, name }: { slug: string, name: string }) {
  let hash = 0
  for (const ch of slug) hash = (hash * 31 + ch.codePointAt(0)!) >>> 0
  const hue = hash % 360

  return (
    <div
      className="grid h-8 w-8 shrink-0 place-items-center rounded-md text-[13px] font-bold text-white"
      style={{ background: `oklch(0.55 0.15 ${hue})` }}
    >
      {(name[0] ?? "?").toUpperCase()}
    </div>
  )
}
