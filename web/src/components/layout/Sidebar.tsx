import { Link } from "@tanstack/react-router"
import { Copyright, Database, ExternalLink, LayoutDashboard, LogOut, RefreshCw, Search, Server, Settings, Shield } from "lucide-react"
import type { LucideIcon } from "lucide-react"
import { ThemeControl } from "@/components/layout/ThemeControl"
import { Badge } from "@/components/ui/badge"
import { Logo } from "@/components/ui/Logo"
import { useAuth } from "@/hooks/useAuth"

type NavItem = {
  to: string
  label: string
  Icon: LucideIcon
  count?: number
}

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

function NavGroup({ title, items }: { title: string; items: NavItem[] }) {
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
        <Logo className="h-6 w-6" />
        <span className="text-[15px] font-semibold tracking-tight">harbrr</span>
      </div>

      <nav className="flex flex-1 flex-col gap-6 px-3 py-4">
        <div className="flex flex-col gap-1">
          <NavLink to="/" label="Dashboard" Icon={LayoutDashboard} />
        </div>
        <NavGroup title="Manage" items={MANAGE} />
        <NavGroup title="Sync" items={SYNC} />
        <div className="mt-auto flex flex-col gap-1">
          <NavLink to="/settings" label="Settings" Icon={Settings} />
        </div>
      </nav>

      <div className="flex flex-col border-t border-sidebar-border px-3 py-3">
        <UserChip />
        <div className="mt-2 flex items-center justify-between px-2.5 pt-1">
          <div className="flex select-none flex-col gap-0.5 text-[10px] text-faint">
            {window.__HARBRR_VERSION__ && (
              <span className="font-medium">
                Version {window.__HARBRR_VERSION__.split(" ")[0]}
              </span>
            )}
            <div className="flex items-center gap-1">
              <Copyright className="h-2.5 w-2.5" />
              <span>{new Date().getFullYear()} harbrr</span>
            </div>
          </div>
          <a
            href="https://github.com/autobrr/harbrr"
            target="_blank"
            rel="noreferrer"
            aria-label="GitHub"
            className="text-faint transition hover:text-muted-foreground"
          >
            <ExternalLink className="h-3.5 w-3.5" />
          </a>
        </div>
      </div>
    </aside>
  )
}

function UserChip() {
  const { user, authDisabled, logout } = useAuth()

  return (
    <div className="flex items-center gap-2 px-1.5 py-1">
      {user && !authDisabled && (
        <button
          type="button"
          aria-label="Log out"
          onClick={() => logout.mutate()}
          className="flex flex-1 items-center gap-2 rounded-md px-2 py-1.5 text-[13px] text-muted-foreground transition hover:bg-sidebar-accent hover:text-sidebar-foreground"
        >
          <LogOut className="h-4 w-4" />
          Log out
        </button>
      )}
      <ThemeControl />
    </div>
  )
}
