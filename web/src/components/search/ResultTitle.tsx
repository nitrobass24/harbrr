import type { Release } from "@/lib/api"
import { isSafeHref } from "@/lib/safe-href"
import { cn } from "@/lib/utils"

export function ResultTitle({ release, className }: { release: Release, className?: string }) {
  if (!isSafeHref(release.details, ["http:", "https:"])) {
    return <span className={className} title={release.title}>{release.title}</span>
  }

  return (
    <a
      href={release.details}
      target="_blank"
      rel="noopener noreferrer"
      className={cn("hover:underline focus-visible:underline", className)}
      title={release.title}
    >
      {release.title}
    </a>
  )
}
