import { Monitor, Moon, Sun } from "lucide-react"
import { useTheme } from "next-themes"
import { cn } from "@/lib/utils"

const MODES = [
  { value: "light", label: "Light theme", Icon: Sun },
  { value: "dark", label: "Dark theme", Icon: Moon },
  { value: "system", label: "System theme", Icon: Monitor },
] as const

// Segmented Light / Dark / System control for the sidebar footer.
export function ThemeControl() {
  const { theme, setTheme } = useTheme()

  return (
    <div className="flex items-center gap-0.5 rounded-md border border-sidebar-border p-0.5">
      {MODES.map(({ value, label, Icon }) => (
        <button
          key={value}
          type="button"
          aria-label={label}
          aria-pressed={theme === value}
          onClick={() => setTheme(value)}
          className={cn(
            "grid h-6 w-6 cursor-pointer place-items-center rounded-sm text-muted-foreground transition hover:text-foreground",
            theme === value && "bg-sidebar-accent text-foreground"
          )}
        >
          <Icon className="h-3.5 w-3.5" />
        </button>
      ))}
    </div>
  )
}
