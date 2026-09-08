import type { ReactNode } from "react"

// Centered single-card page shared by the login and setup screens. This is the only
// card in the app, so it carries the shadcn card classes directly rather than through
// a seven-export component set — `bunx shadcn add card` brings that back if a second
// page ever needs it. Classes and data-slots are verbatim what <Card> rendered.
export function AuthCard({ title, description, children }: {
  title: string
  description: string
  children: ReactNode
}) {
  return (
    <main className="grid min-h-screen place-items-center px-4">
      <div
        data-slot="card"
        className="bg-card text-card-foreground flex flex-col gap-6 rounded-xl border py-6 shadow-sm w-full max-w-sm"
      >
        <div
          data-slot="card-header"
          className="@container/card-header grid auto-rows-min grid-rows-[auto_auto] items-start gap-1.5 px-6 has-data-[slot=card-action]:grid-cols-[1fr_auto] [.border-b]:pb-6"
        >
          <div className="mb-2 flex items-center gap-2.5">
            <div className="grid h-8 w-8 place-items-center rounded-md bg-primary text-[14px] font-bold text-primary-foreground">
              h
            </div>
            <span className="text-[16px] font-semibold tracking-tight">harbrr</span>
          </div>
          <div data-slot="card-title" className="leading-none font-semibold">{title}</div>
          <div data-slot="card-description" className="text-muted-foreground text-sm">{description}</div>
        </div>
        <div data-slot="card-content" className="px-6">{children}</div>
      </div>
    </main>
  )
}
