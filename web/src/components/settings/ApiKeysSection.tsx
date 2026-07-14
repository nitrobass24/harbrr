import { useState } from "react"
import { Copy, Plus, Trash2 } from "lucide-react"
import { toast } from "sonner"
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
import { useApiKeys, useMintApiKey, useRevokeApiKey } from "@/hooks/useSettings"
import { copyText } from "@/lib/clipboard"
import { relativeTime } from "@/lib/format"
import type { MintedApiKey } from "@/lib/api"

// Torznab/API keys for consumers (*arr, autobrr, cross-seed). The plaintext
// key exists in the response of the mint call ONLY — this section shows it
// exactly once and never again (the server stores only a hash).
export function ApiKeysSection() {
  const keys = useApiKeys()
  const mint = useMintApiKey()
  const revoke = useRevokeApiKey()
  const [name, setName] = useState("")
  const [minted, setMinted] = useState<MintedApiKey | null>(null)

  return (
    <section id="apikeys" className="flex flex-col gap-3">
      <h2 className="text-[14px] font-semibold tracking-tight">API keys</h2>

      <form
        className="flex items-center gap-2"
        onSubmit={(e) => {
          e.preventDefault()
          mint.mutate(name, {
            onSuccess: (key) => {
              setMinted(key)
              setName("")
            },
            onError: () => toast.error("Minting failed"),
          })
        }}
      >
        <Input
          className="h-9 w-56"
          placeholder="Key name (e.g. sonarr)"
          value={name}
          onChange={(e) => setName(e.target.value)}
        />
        <Button type="submit" size="sm" disabled={mint.isPending || name === ""}>
          <Plus className="h-3.5 w-3.5" /> Mint key
        </Button>
      </form>

      <div className="flex flex-col rounded-xl border border-border bg-card px-5 py-2 text-[13px]">
        {(keys.data ?? []).map((k) => (
          <div key={k.id} className="flex items-center gap-3 border-b border-border/60 py-2.5 last:border-b-0">
            <span className="font-medium">{k.name}</span>
            <span className="text-[12px] text-faint">created {relativeTime(k.createdAt)}</span>
            <span className="text-[12px] text-faint">
              {k.lastUsedAt ? `last used ${relativeTime(k.lastUsedAt)}` : "never used"}
            </span>
            <Button
              variant="ghost"
              size="icon"
              className="ml-auto"
              aria-label={`Revoke ${k.name}`}
              onClick={() => revoke.mutate(k.id, {
                onSuccess: () => toast.success(`${k.name} revoked — consumers using it stop working now`),
              })}
            >
              <Trash2 className="h-4 w-4" />
            </Button>
          </div>
        ))}
        {keys.data?.length === 0 && (
          <p className="py-3 text-muted-foreground">No keys yet — feeds need one for apps to authenticate.</p>
        )}
      </div>

      <Dialog open={minted !== null} onOpenChange={(open) => { if (!open) setMinted(null) }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Key minted — copy it now</DialogTitle>
            <DialogDescription>
              This is the only time the key is shown; harbrr stores just a hash of it.
            </DialogDescription>
          </DialogHeader>
          {minted && (
            <code data-testid="minted-key" className="break-all rounded-md border border-border bg-muted p-3 font-mono text-[12px]">
              {minted.key}
            </code>
          )}
          <DialogFooter>
            <Button
              onClick={() => {
                void copyText(minted?.key ?? "", "Key copied")
              }}
            >
              <Copy className="h-4 w-4" /> Copy key
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </section>
  )
}
