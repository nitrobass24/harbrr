import { Outlet } from "@tanstack/react-router"
import { Sidebar } from "@/components/layout/Sidebar"
import { Toaster } from "@/components/ui/sonner"

// The application shell: fixed sidebar + scrollable content (mockup layout).
export function AppLayout() {
  return (
    <div className="flex h-screen w-full overflow-hidden">
      <Sidebar />
      <main className="min-w-0 flex-1 overflow-auto">
        <Outlet />
      </main>
      <Toaster />
    </div>
  )
}
