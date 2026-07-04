import { Link } from "@tanstack/react-router"

export function NotFound() {
  return (
    <main className="grid min-h-screen place-items-center">
      <div className="text-center">
        <h1 className="text-2xl font-semibold tracking-tight">404</h1>
        <p className="mt-1 text-sm text-muted-foreground">This page does not exist.</p>
        <Link to="/" className="mt-4 inline-block text-sm text-primary hover:underline">
          Back to the dashboard
        </Link>
      </div>
    </main>
  )
}
