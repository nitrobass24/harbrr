import { useState } from "react"
import { notifySuccess } from "@/lib/notify"
import { Input } from "@/components/ui/input"
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import { DefinitionOption, IndexerForm, type IndexerFormSubmit } from "@/components/indexers/form/IndexerForm"
import { useDefinition, useDefinitions } from "@/hooks/useDefinitions"
import { useAddIndexer, useIndexer, useTestIndexer, useUpdateIndexer } from "@/hooks/useIndexers"

export type IndexerSheetState =
  | { open: false }
  | { open: true, mode: "create" }
  | { open: true, mode: "edit", slug: string }

// Add flow: definition picker → schema-driven form. Edit flow: the stored
// instance's definition drives the same form with stored settings prefilled.
// A successful save immediately tests the indexer and toasts the outcome.
export function IndexerSheet({ state, onClose }: { state: IndexerSheetState, onClose: () => void }) {
  return (
    <Sheet open={state.open} onOpenChange={(open) => { if (!open) onClose() }}>
      <SheetContent side="right" className="w-full overflow-auto sm:max-w-lg">
        {state.open && (state.mode === "create" ? <CreateFlow onClose={onClose} /> : <EditFlow key={state.slug} slug={state.slug} onClose={onClose} />)}
      </SheetContent>
    </Sheet>
  )
}

function useSaveAndTest(onClose: () => void) {
  // toastResult: true — the sheet closes (unmounts) right after mutate() fires,
  // so the pass/fail toast must be hook-level to survive that unmount (see
  // useTestIndexer in useIndexers.ts).
  const test = useTestIndexer({ toastResult: true })
  return (slug: string, verb: string) => {
    notifySuccess(`${slug} ${verb} — testing…`)
    test.mutate(slug)
    onClose()
  }
}

function CreateFlow({ onClose }: { onClose: () => void }) {
  const [definitionId, setDefinitionId] = useState<string | null>(null)
  const [filter, setFilter] = useState("")
  const definitions = useDefinitions()
  const definition = useDefinition(definitionId)
  const add = useAddIndexer()
  const saved = useSaveAndTest(onClose)

  if (definitionId === null) {
    const needle = filter.toLowerCase()
    const matches = (definitions.data ?? [])
      .filter((d) => d.name.toLowerCase().includes(needle) || d.id.includes(needle))
      .slice(0, 50)
    return (
      <>
        <SheetHeader>
          <SheetTitle>Add indexer</SheetTitle>
          <SheetDescription>Pick a tracker definition ({definitions.data?.length ?? "…"} available).</SheetDescription>
        </SheetHeader>
        <div className="flex min-h-0 flex-1 flex-col gap-2 px-4 pb-6">
          <Input
            placeholder="Filter definitions"
            autoFocus
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
          />
          <div className="min-h-0 flex-1 overflow-auto">
            {matches.map((d) => (
              <DefinitionOption key={d.id} {...d} onPick={setDefinitionId} />
            ))}
            {definitions.data && matches.length === 0 && (
              <p className="px-3 py-2 text-[13px] text-muted-foreground">No definitions match.</p>
            )}
          </div>
        </div>
      </>
    )
  }

  return (
    <>
      <SheetHeader>
        <SheetTitle>Add {definition.data?.name ?? definitionId}</SheetTitle>
        <SheetDescription>
          <button type="button" className="cursor-pointer text-primary hover:underline" onClick={() => setDefinitionId(null)}>
            ← pick a different definition
          </button>
        </SheetDescription>
      </SheetHeader>
      <div className="px-4 pb-6">
        {definition.data ? (
          <IndexerForm
            definition={definition.data}
            pending={add.isPending}
            error={add.error}
            onSubmit={(submit: IndexerFormSubmit) => {
              if (submit.mode !== "create") return
              add.mutate(submit.body, { onSuccess: (ix) => saved(ix.slug, "added") })
            }}
          />
        ) : (
          <p className="text-[13px] text-muted-foreground">Loading definition…</p>
        )}
      </div>
    </>
  )
}

function EditFlow({ slug, onClose }: { slug: string, onClose: () => void }) {
  const instance = useIndexer(slug)
  const definition = useDefinition(instance.data?.definitionId ?? null)
  const update = useUpdateIndexer(slug)
  const saved = useSaveAndTest(onClose)

  return (
    <>
      <SheetHeader>
        <SheetTitle>Edit {slug}</SheetTitle>
        <SheetDescription>Secrets read back as the redacted sentinel; leave them untouched to keep the stored value.</SheetDescription>
      </SheetHeader>
      <div className="px-4 pb-6">
        {instance.data && definition.data ? (
          <IndexerForm
            definition={definition.data}
            existing={instance.data}
            pending={update.isPending}
            error={update.error}
            onSubmit={(submit: IndexerFormSubmit) => {
              if (submit.mode !== "edit") return
              update.mutate(submit.body, { onSuccess: () => saved(slug, "updated") })
            }}
          />
        ) : (
          <p className="text-[13px] text-muted-foreground">Loading…</p>
        )}
      </div>
    </>
  )
}
