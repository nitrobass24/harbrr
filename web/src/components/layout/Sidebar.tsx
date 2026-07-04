import { Link } from "@tanstack/react-router"
import { Database, LayoutDashboard, LogOut, RefreshCw, Search, Server, Settings, Shield } from "lucide-react"
import type { LucideIcon } from "lucide-react"
import { ThemeControl } from "@/components/layout/ThemeControl"
import { Badge } from "@/components/ui/badge"
import { useAuth } from "@/hooks/useAuth"

type NavItem = {
  to: string
  label: string
  Icon: LucideIcon
  count?: number
}

// Nav shape per the mockup shell (docs/webui-scope.md §3): Dashboard on top,
// then the Manage and Sync groups; Settings lives in the footer.
const MANAGE: NavItem[] = [
  { to: "/indexers", label: "Indexers", Icon: Server },
  { to: "/cache", label: "Cache", Icon: Database },
  { to: "/resources", label: "Proxies & Solvers", Icon: Shield },
  { to: "/search", label: "Search", Icon: Search },
]
const SYNC: NavItem[] = [
  { to: "/applications", label: "Applications", Icon: RefreshCw },
]

function NavLink({ to, label, Icon, count }: NavItem) {
  return (
    <Link
      to={to}
      className="flex items-center gap-2.5 rounded-md px-2.5 py-2 text-[13px] text-muted-foreground transition hover:bg-sidebar-accent hover:text-sidebar-foreground"
      activeProps={{
        className: "bg-sidebar-accent font-medium text-sidebar-foreground",
      }}
    >
      <Icon className="h-4 w-4" />
      {label}
      {count !== undefined && (
        <Badge variant="secondary" className="ml-auto px-1.5 py-0 text-[11px]">
          {count}
        </Badge>
      )}
    </Link>
  )
}

function NavGroup({ title, items }: { title: string, items: NavItem[] }) {
  return (
    <div className="flex flex-col gap-1">
      <div className="px-2 pb-1 text-[11px] font-medium uppercase tracking-wider text-faint">{title}</div>
      {items.map((item) => <NavLink key={item.to} {...item} />)}
    </div>
  )
}

export function Sidebar() {
  return (
    <aside className="flex w-60 shrink-0 flex-col border-r border-sidebar-border bg-sidebar text-sidebar-foreground">
      <div className="flex h-14 items-center gap-2.5 px-5">
        <div className="grid h-7 w-7 place-items-center rounded-md bg-sidebar-primary text-[13px] font-bold text-sidebar-primary-foreground">
          h
        </div>
        <span className="text-[15px] font-semibold tracking-tight">harbrr</span>
        {window.__HARBRR_VERSION__ && (
          <span className="ml-auto rounded border border-sidebar-border px-1.5 py-0.5 text-[10px] font-medium text-faint">
            {window.__HARBRR_VERSION__}
          </span>
        )}
      </div>

      <nav className="flex flex-1 flex-col gap-6 px-3 py-4">
        <div className="flex flex-col gap-1">
          <NavLink to="/" label="Dashboard" Icon={LayoutDashboard} />
        </div>
        <NavGroup title="Manage" items={MANAGE} />
        <NavGroup title="Sync" items={SYNC} />
      </nav>

      <div className="flex flex-col gap-1 border-t border-sidebar-border px-3 py-3">
        <NavLink to="/settings" label="Settings" Icon={Settings} />
        <UserChip />
      </div>
    </aside>
  )
}

// Signed-in identity + logout + theme control. When auth is disabled there is
// no account, so only the theme control renders.
function UserChip() {
  const { user, authDisabled, logout } = useAuth()

  return (
    <div className="flex items-center gap-2.5 px-2.5 pt-1">
      {user && !authDisabled && (
        <>
          <div className="grid h-7 w-7 shrink-0 place-items-center rounded-full bg-sidebar-accent text-[12px] font-semibold text-muted-foreground">
            {user.username.slice(0, 2).toUpperCase()}
          </div>
          <span className="truncate text-[12px] font-medium">{user.username}</span>
          <button
            type="button"
            aria-label="Log out"
            onClick={() => logout.mutate()}
            className="grid h-6 w-6 shrink-0 cursor-pointer place-items-center rounded-md text-muted-foreground transition hover:bg-sidebar-accent hover:text-foreground"
          >
            <LogOut className="h-3.5 w-3.5" />
          </button>
        </>
      )}
      <div className="ml-auto">
        <ThemeControl />
      </div>
    </div>
  )
}
