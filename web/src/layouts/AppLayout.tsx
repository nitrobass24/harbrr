import { Outlet } from "@tanstack/react-router"
import { MobileFooterNav } from "@/components/layout/MobileFooterNav"
import { Sidebar } from "@/components/layout/Sidebar"
import { Toaster } from "@/components/ui/sonner"

// The application shell: fixed sidebar + scrollable content (mockup layout).
// Below the md breakpoint the sidebar hides in favor of a fixed bottom nav.
export function AppLayout() {
  return (
    <div className="flex h-screen w-full overflow-hidden">
      <Sidebar />
      <main className="min-w-0 flex-1 overflow-auto pb-16 md:pb-0">
        <Outlet />
      </main>
      <MobileFooterNav />
      <Toaster />
    </div>
  )
}
