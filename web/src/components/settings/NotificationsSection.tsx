import { useState } from "react"
import { Plus, Trash2 } from "lucide-react"
import { toast } from "sonner"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { NativeSelect } from "@/components/ui/native-select"
import { Switch } from "@/components/ui/switch"
import { useNotificationMutations, useNotifications } from "@/hooks/useSettings"

// Webhook/Discord targets for operational events (indexer health failures).
// The destination URL is a secret (may embed tokens): stored encrypted, reads
// back as the sentinel, and rotates only when a new URL is typed.
export function NotificationsSection() {
  const notifications = useNotifications()
  const { create, remove, toggle, test } = useNotificationMutations()
  const [adding, setAdding] = useState(false)
  const [name, setName] = useState("")
  const [type, setType] = useState<"webhook" | "discord">("discord")
  const [url, setUrl] = useState("")

  return (
    <section id="notifications" className="flex flex-col gap-3">
      <div className="flex items-center gap-3">
        <h2 className="text-[14px] font-semibold tracking-tight">Notifications</h2>
        <Button variant="outline" size="sm" className="ml-auto" onClick={() => setAdding(true)}>
          <Plus className="h-3.5 w-3.5" /> Add target
        </Button>
      </div>

      <div className="flex flex-col rounded-xl border border-border bg-card px-5 py-2 text-[13px]">
        {(notifications.data ?? []).map((n) => (
          <div key={n.id} className="flex items-center gap-3 border-b border-border/60 py-2.5 last:border-b-0">
            <span className="font-medium">{n.name}</span>
            <Badge variant="secondary" className="px-1.5 py-0 text-[11px]">{n.type}</Badge>
            {n.onHealthFailure && <span className="text-[12px] text-faint">on health failure</span>}
            <span className="ml-auto flex items-center gap-2">
              <Button
                variant="outline"
                size="sm"
                onClick={() => test.mutate(n.id, {
                  onSuccess: (r) => r.ok ? toast.success("Test notification sent") : toast.error(`Test failed — ${r.error ?? ""}`),
                  onError: () => toast.error("Test request failed"),
                })}
              >
                Test
              </Button>
              <Switch
                aria-label={`${n.enabled ? "Disable" : "Enable"} ${n.name}`}
                checked={n.enabled}
                onCheckedChange={(checked) => toggle.mutate({ id: n.id, enabled: checked })}
              />
              <Button variant="ghost" size="icon" aria-label={`Delete ${n.name}`} onClick={() => remove.mutate(n.id)}>
                <Trash2 className="h-4 w-4" />
              </Button>
            </span>
          </div>
        ))}
        {notifications.data?.length === 0 && (
          <p className="py-3 text-muted-foreground">No notification targets.</p>
        )}
      </div>

      <Dialog
        open={adding}
        onOpenChange={(open) => {
          setAdding(open)
          // Reset the form on any close (submit, escape, or outside click) so a
          // dismissed dialog does not reopen with stale values.
          if (!open) {
            setName("")
            setUrl("")
            setType("discord")
          }
        }}
      >
        <DialogContent>
          <form
            className="flex flex-col gap-4"
            onSubmit={(e) => {
              e.preventDefault()
              create.mutate({ name, type, url }, {
                onSuccess: () => {
                  setAdding(false)
                },
                onError: (err) => toast.error(`Adding failed: ${err.message}`),
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
        </DialogContent>
      </Dialog>
    </section>
  )
}
