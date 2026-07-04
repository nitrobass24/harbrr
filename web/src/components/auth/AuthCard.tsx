import type { ReactNode } from "react"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"

// Centered single-card page shared by the login and setup screens.
export function AuthCard({ title, description, children }: {
  title: string
  description: string
  children: ReactNode
}) {
  return (
    <main className="grid min-h-screen place-items-center px-4">
      <Card className="w-full max-w-sm">
        <CardHeader>
          <div className="mb-2 flex items-center gap-2.5">
            <div className="grid h-8 w-8 place-items-center rounded-md bg-primary text-[14px] font-bold text-primary-foreground">
              h
            </div>
            <span className="text-[16px] font-semibold tracking-tight">harbrr</span>
          </div>
          <CardTitle>{title}</CardTitle>
          <CardDescription>{description}</CardDescription>
        </CardHeader>
        <CardContent>{children}</CardContent>
      </Card>
    </main>
  )
}
