import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { NativeSelect } from "@/components/ui/native-select"
import { isInfoField } from "@/components/indexers/form/settings-payload"
import type { SettingField } from "@/types/api"

// Renders one definition setting by its schema type: text | password |
// checkbox | select | multi-select | info*. Secret fields are masked; their
// prefilled <redacted> sentinel rides along untouched unless the operator
// types a replacement (settings-payload.ts owns that contract).
export function SettingFieldInput({ field, value, onChange }: {
  field: SettingField
  value: string
  onChange: (value: string) => void
}) {
  if (isInfoField(field)) {
    return (
      <div className="rounded-md border border-border bg-muted/50 px-3 py-2 text-[12px] whitespace-pre-wrap text-muted-foreground">
        {field.label && <span className="font-medium">{field.label}: </span>}
        {field.default ?? ""}
      </div>
    )
  }

  const label = field.label ?? field.name
  const id = `setting-${field.name}`

  switch (field.type) {
    case "checkbox":
      return (
        <div className="flex items-center gap-2">
          <Checkbox
            id={id}
            checked={value === "true"}
            onCheckedChange={(checked) => onChange(checked === true ? "true" : "false")}
          />
          <Label htmlFor={id} className="font-normal">{label}</Label>
        </div>
      )
    case "select":
      return (
        <Field id={id} label={label}>
          <NativeSelect id={id} value={value} onChange={(e) => onChange(e.target.value)}>
            {field.default === undefined && <option value="">—</option>}
            {Object.entries(field.options ?? {}).map(([key, name]) => (
              <option key={key} value={key}>{name}</option>
            ))}
          </NativeSelect>
        </Field>
      )
    case "multi-select": {
      const selected = new Set(value === "" ? [] : value.split(","))
      return (
        <Field id={id} label={label}>
          <div className="flex flex-col gap-1.5">
            {Object.entries(field.options ?? {}).map(([key, name]) => (
              <div key={key} className="flex items-center gap-2">
                <Checkbox
                  id={`${id}-${key}`}
                  checked={selected.has(key)}
                  onCheckedChange={(checked) => {
                    const next = new Set(selected)
                    if (checked === true) next.add(key)
                    else next.delete(key)
                    onChange([...next].join(","))
                  }}
                />
                <Label htmlFor={`${id}-${key}`} className="font-normal">{name}</Label>
              </div>
            ))}
          </div>
        </Field>
      )
    }
    default: // text, password, and any unknown future type degrades to text
      return (
        <Field id={id} label={label}>
          <Input
            id={id}
            type={field.type === "password" || field.secret ? "password" : "text"}
            autoComplete="off"
            value={value}
            onChange={(e) => onChange(e.target.value)}
          />
        </Field>
      )
  }
}

function Field({ id, label, children }: { id: string, label: string, children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-1.5">
      <Label htmlFor={id}>{label}</Label>
      {children}
    </div>
  )
}
