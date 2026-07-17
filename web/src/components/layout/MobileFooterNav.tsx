/*
 * Copyright (c) 2025-2026, s0up and the autobrr contributors.
 * SPDX-License-Identifier: GPL-2.0-or-later
 */

import { Link, useLocation } from "@tanstack/react-router"
import { Database, HardDriveDownload, LayoutDashboard, MoreHorizontal, RefreshCw, Search, Server, Settings, Shield } from "lucide-react"
import type { LucideIcon } from "lucide-react"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger
} from "@/components/ui/dropdown-menu"
import { cn } from "@/lib/utils"

type NavItem = {
  to: string
  label: string
  Icon: LucideIcon
}

const PRIMARY: NavItem[] = [
  { to: "/", label: "Dashboard", Icon: LayoutDashboard },
  { to: "/indexers", label: "Indexers", Icon: Server },
  { to: "/applications", label: "Applications", Icon: RefreshCw },
  { to: "/search", label: "Search", Icon: Search },
]

const OVERFLOW: NavItem[] = [
  { to: "/settings", label: "Settings", Icon: Settings },
  { to: "/cache", label: "Cache", Icon: Database },
  { to: "/resources", label: "Proxies & Solvers", Icon: Shield },
  { to: "/download-clients", label: "Download Clients", Icon: HardDriveDownload },
]

function FooterLink({ to, label, Icon, active }: NavItem & { active: boolean }) {
  return (
    <Link
      to={to}
      className={cn(
        "flex min-w-0 flex-1 flex-col items-center justify-center gap-1 px-3 py-2 text-xs font-medium transition-colors",
        active ? "text-primary" : "text-muted-foreground hover:text-foreground"
      )}
    >
      <Icon className="h-5 w-5" />
      <span className="truncate">{label}</span>
    </Link>
  )
}

// Bottom nav bar shown on mobile viewports only; mirrors the Sidebar's 8 destinations
// with the overflow items folded into a "More" dropdown.
export function MobileFooterNav() {
  const location = useLocation()
  const isOverflowActive = OVERFLOW.some((item) => item.to === location.pathname)

  return (
    <nav
      data-testid="mobile-footer-nav"
      className="fixed inset-x-0 bottom-0 z-40 border-t border-sidebar-border bg-sidebar text-sidebar-foreground md:hidden"
      style={{ paddingBottom: "env(safe-area-inset-bottom)" }}
    >
      <div className="flex h-16 items-center justify-around">
        {PRIMARY.map((item) => (
          <FooterLink key={item.to} {...item} active={location.pathname === item.to} />
        ))}
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <button
              type="button"
              className={cn(
                "flex min-w-0 flex-1 flex-col items-center justify-center gap-1 px-3 py-2 text-xs font-medium transition-colors",
                isOverflowActive ? "text-primary" : "text-muted-foreground hover:text-foreground"
              )}
            >
              <MoreHorizontal className="h-5 w-5" />
              <span className="truncate">More</span>
            </button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" side="top" className="mb-2 w-48">
            {OVERFLOW.map(({ to, label, Icon }) => (
              <DropdownMenuItem key={to} asChild>
                <Link to={to} className="flex items-center gap-2">
                  <Icon className="h-4 w-4" />
                  {label}
                </Link>
              </DropdownMenuItem>
            ))}
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
    </nav>
  )
}
