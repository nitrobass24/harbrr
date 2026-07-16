import type { ReactNode } from "react"

export function PageHeader({ title, subtitle, children }: {
  title: string
  subtitle?: string
  children?: ReactNode
}) {
  return (
    <header className="flex h-14 shrink-0 items-center gap-4 border-b border-border px-4 md:px-7">
      <div className="flex flex-col">
        <h1 className="text-2xl font-bold leading-tight tracking-tight">{title}</h1>
        {subtitle && <p className="text-[12px] text-faint">{subtitle}</p>}
      </div>
      {children && <div className="ml-auto flex items-center gap-2.5">{children}</div>}
    </header>
  )
}
