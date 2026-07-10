import { cn } from "@/lib/utils"

export function ProtocolPill({ protocol }: { protocol?: string }) {
  const isUsenet = protocol === "usenet"
  return (
    <span
      className={cn(
        "inline-flex items-center rounded-full border px-2 py-0.5 text-[11px] font-medium",
        isUsenet
          ? "border-brand/40 bg-brand/10 text-brand"
          : "border-border bg-muted text-muted-foreground"
      )}
    >
      {isUsenet ? "NZB" : "Torrent"}
    </span>
  )
}
