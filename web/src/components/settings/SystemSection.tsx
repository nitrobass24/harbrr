import { useState } from "react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { NativeSelect } from "@/components/ui/native-select"
import { Switch } from "@/components/ui/switch"
import { useAuth } from "@/hooks/useAuth"
import {
  useAdultCategories,
  useAllIndexerStats,
  useChangePassword,
  useHealth,
  useLogLevel,
  useSetAdultCategories,
  useSetLogLevel
} from "@/hooks/useSettings"
import { getBaseUrl } from "@/lib/base-url"
import { relativeTime } from "@/lib/format"
import { notifyError, notifySuccess } from "@/lib/notify"
import type { LogLevel } from "@/lib/api"

const LEVELS: LogLevel[] = ["trace", "debug", "info", "warn", "error"]

// Categories, logging, account, and About stitched into one system block.
export function SystemSection() {
  const { authDisabled } = useAuth()
  return (
    <>
      <CategoriesBlock />
      <LoggingBlock />
      {!authDisabled && <AccountBlock />}
      <AboutBlock />
    </>
  )
}

// CategoriesBlock is the global hide-adult-categories switch (autobrr/harbrr#383).
// The copy under it states the limitation plainly: harbrr filters by the category
// a tracker declares, so a miscategorised release still gets through. This is not
// a content guarantee and must never be presented as one.
function CategoriesBlock() {
  const setting = useAdultCategories()
  const setHidden = useSetAdultCategories()

  return (
    <section id="categories" className="flex flex-col gap-3">
      <h2 className="text-[14px] font-semibold tracking-tight">Categories</h2>
      <div className="flex flex-col gap-2 rounded-xl border border-border bg-card px-5 py-4 text-[13px]">
        <div className="flex items-center gap-3">
          <Switch
            id="hide-adult-categories"
            aria-label="Hide adult categories"
            checked={setting.data?.hidden ?? false}
            disabled={setting.isPending || setHidden.isPending}
            onCheckedChange={(checked) => setHidden.mutate(checked, {
              onSuccess: (r) => notifySuccess(r.hidden ? "Adult categories hidden" : "Adult categories shown"),
              onError: (err) => notifyError("Changing the adult-category setting failed", err),
            })}
          />
          <Label htmlFor="hide-adult-categories">Hide adult categories</Label>
        </div>
        <p className="text-[12px] text-faint">
          Removes the XXX categories from category pickers and capability lists, and drops
          adult-category results from searches that do not ask for a category. It filters by
          the category a tracker declares, so a release the tracker filed under something else
          can still appear — this hides a taxonomy, it is not a content filter. Torznab and
          Newznab feeds are not affected: apps like Sonarr and Radarr send their own categories.
        </p>
      </div>
    </section>
  )
}

function LoggingBlock() {
  const level = useLogLevel()
  const setLevel = useSetLogLevel()

  return (
    <section id="logging" className="flex flex-col gap-3">
      <h2 className="text-[14px] font-semibold tracking-tight">Logging</h2>
      <div className="flex items-center gap-3 rounded-xl border border-border bg-card px-5 py-4 text-[13px]">
        <Label htmlFor="log-level">Level (applies live, survives restarts)</Label>
        <NativeSelect
          id="log-level"
          className="w-32"
          value={level.data?.level ?? "info"}
          onChange={(e) => setLevel.mutate(e.target.value as LogLevel, {
            onSuccess: (r) => notifySuccess(`Log level set to ${r.level}`),
            onError: (err) => notifyError("Setting the log level failed", err),
          })}
        >
          {LEVELS.map((l) => <option key={l} value={l}>{l}</option>)}
        </NativeSelect>
      </div>
    </section>
  )
}

function AccountBlock() {
  const change = useChangePassword()
  const [current, setCurrent] = useState("")
  const [next, setNext] = useState("")
  const [confirm, setConfirm] = useState("")
  const mismatch = confirm !== "" && confirm !== next

  return (
    <section id="account" className="flex flex-col gap-3">
      <h2 className="text-[14px] font-semibold tracking-tight">Account</h2>
      <form
        className="flex flex-col gap-3 rounded-xl border border-border bg-card px-5 py-4"
        onSubmit={(e) => {
          e.preventDefault()
          change.mutate({ current, next }, {
            onSuccess: () => {
              notifySuccess("Password changed")
              setCurrent("")
              setNext("")
              setConfirm("")
            },
            onError: (err) => notifyError(`Change failed: ${err.message}`, err),
          })
        }}
      >
        <div className="grid grid-cols-3 gap-3">
          <span className="flex flex-col gap-1.5">
            <Label htmlFor="pw-current">Current password</Label>
            <Input id="pw-current" type="password" autoComplete="current-password" value={current} onChange={(e) => setCurrent(e.target.value)} />
          </span>
          <span className="flex flex-col gap-1.5">
            <Label htmlFor="pw-new">New password</Label>
            <Input id="pw-new" type="password" autoComplete="new-password" value={next} onChange={(e) => setNext(e.target.value)} />
          </span>
          <span className="flex flex-col gap-1.5">
            <Label htmlFor="pw-confirm">Confirm new password</Label>
            <Input id="pw-confirm" type="password" autoComplete="new-password" value={confirm} onChange={(e) => setConfirm(e.target.value)} />
            {mismatch && <p className="text-[12px] text-bad">Passwords do not match.</p>}
          </span>
        </div>
        <div>
          <Button type="submit" size="sm" disabled={change.isPending || !current || !next || next !== confirm}>
            {change.isPending ? "Changing…" : "Change password"}
          </Button>
        </div>
      </form>
    </section>
  )
}

function AboutBlock() {
  const health = useHealth()
  const stats = useAllIndexerStats()

  return (
    <section id="about" className="flex flex-col gap-3">
      <h2 className="text-[14px] font-semibold tracking-tight">About</h2>
      <div className="flex flex-col gap-3 rounded-xl border border-border bg-card px-5 py-4 text-[13px]">
        <dl className="grid grid-cols-2 gap-x-4 gap-y-1.5 sm:grid-cols-4">
          <dt className="text-muted-foreground">Version</dt>
          <dd className="font-mono">{health.data?.version ?? "—"}</dd>
          <dt className="text-muted-foreground">Commit</dt>
          <dd className="font-mono">{health.data?.commit ?? "—"}</dd>
        </dl>
        <p className="text-[12px] text-faint">
          The full management API is interactive at{" "}
          <a className="text-primary hover:underline" href={`${getBaseUrl()}/api/docs`} target="_blank" rel="noreferrer">
            /api/docs
          </a>{" "}
          (spec at <span className="font-mono">/api/openapi.yaml</span>).
        </p>
        {stats.data && stats.data.length > 0 && (
          <div className="flex flex-col gap-1.5">
            <p className="text-[11px] font-medium uppercase tracking-wider text-faint">Per-indexer stats</p>
            {stats.data.map((s) => (
              <div key={s.slug} className="flex items-baseline gap-3">
                <span className="w-40 truncate font-medium">{s.slug}</span>
                <span className="text-muted-foreground">{s.queries} queries</span>
                <span className="text-muted-foreground">{s.grabs} grabs</span>
                {s.avgResponseMs !== undefined && <span className="text-faint">{s.avgResponseMs} ms avg</span>}
                <span className="ml-auto text-[12px] text-faint">
                  {s.lastQueryAt ? `last ${relativeTime(s.lastQueryAt)}` : "never queried"}
                </span>
              </div>
            ))}
          </div>
        )}
      </div>
    </section>
  )
}
