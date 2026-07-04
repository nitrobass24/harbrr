import * as React from "react"
import { cn } from "@/lib/utils"

// A styled native <select>: dependable inside dynamic schema forms (and in
// jsdom tests) where a portal-based listbox buys nothing.
export function NativeSelect({ className, ...props }: React.ComponentProps<"select">) {
  return (
    <select
      className={cn(
        "border-input h-9 w-full min-w-0 cursor-pointer rounded-md border bg-transparent px-3 py-1 text-sm shadow-xs transition-[color,box-shadow] outline-none",
        "focus-visible:border-ring focus-visible:ring-ring/50 focus-visible:ring-[3px]",
        "disabled:cursor-not-allowed disabled:opacity-50 dark:bg-input/30",
        className
      )}
      {...props}
    />
  )
}
