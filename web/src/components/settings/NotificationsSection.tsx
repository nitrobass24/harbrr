import { useState } from "react"
import { Trash2 } from "lucide-react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { NativeSelect } from "@/components/ui/native-select"
import { ResourceSection } from "@/components/ui/resource-section"
import { Switch } from "@/components/ui/switch"
import { useNotificationMutations, useNotifications } from "@/hooks/useSettings"
import { notifyError, notifySuccess } from "@/lib/notify"
import type { Notification } from "@/lib/api"
import type { MutationDoneHandlers } from "@/components/ui/resource-section"

// Webhook/Discord targets for operational events (indexer health failures and
// VIP/membership expiry warnings — a new target opts into both).
// The destination URL is a secret (may embed tokens): stored encrypted, reads
// back as the sentinel, and rotates only when a new URL is typed.
export function NotificationsSection() {
  const notifications = useNotifications()
  const { remove, toggle, test } = useNotificationMutations()

  return (
    <ResourceSection<Notification>
      id="notifications"
      title="Notifications"
      addLabel="Add target"
      query={notifications}
      empty="No notification targets."
      canEdit={false}
      row={(n) => (
        <>
          <span className="font-medium">{n.name}</span>
          <Badge variant="secondary" className="px-1.5 py-0 text-[11px]">{n.type}</Badge>
          <span className="text-[12px] text-faint">
            {[n.onHealthFailure && "health failure", n.onExpiry && "expiry"].filter(Boolean).join(" · ")}
          </span>
          <span className="ml-auto flex items-center gap-2">
            <Button
              variant="outline"
              size="sm"
              onClick={() => test.mutate(n.id, {
                onSuccess: (r) => r.ok ? notifySuccess("Test notification sent") : notifyError(`Test failed — ${r.error ?? ""}`),
                onError: (err) => notifyError("Test request failed", err),
              })}
            >
              Test
            </Button>
            <Switch
              aria-label={`${n.enabled ? "Disable" : "Enable"} ${n.name}`}
              checked={n.enabled}
              onCheckedChange={(checked) => toggle.mutate({ id: n.id, enabled: checked })}
            />
            {/* Delete stays section-owned (bare, no toasts) — exactly as before the shell. */}
            <Button variant="ghost" size="icon" aria-label={`Delete ${n.name}`} onClick={() => remove.mutate(n.id)}>
              <Trash2 className="h-4 w-4" />
            </Button>
          </span>
        </>
      )}
      form={(_target, done) => <NotificationForm done={done} />}
    />
  )
}

// Add-only (targets are recreated, not edited). The form's state lives here so a
// dismissed dialog never reopens with stale values — the shell remounts it per open.
// It owns its own create mutation: isPending is per-hook-instance.
function NotificationForm({ done }: { done: MutationDoneHandlers }) {
  const { create } = useNotificationMutations()
  const [name, setName] = useState("")
  const [type, setType] = useState<"webhook" | "discord">("discord")
  const [url, setUrl] = useState("")

  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={(e) => {
        e.preventDefault()
        create.mutate({ name, type, url }, {
          onSuccess: done.onSuccess,
          // Add-only wording predates the shell's "Save failed" — kept byte-identical.
          onError: (err) => notifyError(`Adding failed: ${err.message}`, err),
        })
      }}
    >
      <DialogHeader>
        <DialogTitle>Add notification target</DialogTitle>
        <DialogDescription>The destination URL is stored encrypted and never shown again.</DialogDescription>
      </DialogHeader>
      <div className="grid grid-cols-2 gap-3">
        <span className="flex flex-col gap-1.5">
          <Label htmlFor="notif-name">Name</Label>
          <Input id="notif-name" value={name} onChange={(e) => setName(e.target.value)} />
        </span>
        <span className="flex flex-col gap-1.5">
          <Label htmlFor="notif-type">Type</Label>
          <NativeSelect id="notif-type" value={type} onChange={(e) => setType(e.target.value as "webhook" | "discord")}>
            <option value="discord">discord</option>
            <option value="webhook">webhook</option>
          </NativeSelect>
        </span>
      </div>
      <span className="flex flex-col gap-1.5">
        <Label htmlFor="notif-url">Destination URL</Label>
        <Input id="notif-url" type="password" autoComplete="off" placeholder="https://discord.com/api/webhooks/…" value={url} onChange={(e) => setUrl(e.target.value)} />
      </span>
      <DialogFooter>
        <Button type="submit" disabled={create.isPending || !name || !url}>
          {create.isPending ? "Adding…" : "Add target"}
        </Button>
      </DialogFooter>
    </form>
  )
}
