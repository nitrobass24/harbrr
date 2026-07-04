import { cn } from "@/lib/utils"

// Private / Public / Semi-private pill per the mockup's tinted badges.
export function TypePill({ type }: { type?: string }) {
  if (!type) return null

  const label = type === "semi-private" ? "Semi-private" : type.charAt(0).toUpperCase() + type.slice(1)

  return (
    <span
      className={cn(
        "inline-flex items-center rounded-full border px-2 py-0.5 text-[11px] font-medium",
        type === "private" && "border-bad/40 bg-bad/10 text-bad",
        type === "public" && "border-ok/40 bg-ok/10 text-ok",
        type !== "private" && type !== "public" && "border-border bg-muted text-muted-foreground"
      )}
    >
      {label}
    </span>
  )
}
