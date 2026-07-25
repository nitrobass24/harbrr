import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { NativeSelect } from "@/components/ui/native-select"
import { decomposeHostPort, parsePastedURL } from "@/lib/hosturl"

// Shared scheme + Host + Port row for every "add a thing with an address" dialog
// (download clients, app-sync/announce targets, the App edit form). Host accepts a
// reverse-proxy base path ("nginx/sonarr"); Port is a string so "" reads as "use the
// scheme's default" rather than forcing a number. Pasting a full URL into Host fans it
// out into all three fields instead of getting composed a second time.
export function HostPortFields({
  idPrefix, scheme, host, port, onScheme, onHost, onPort, showScheme = true,
}: {
  idPrefix: string
  scheme: "http" | "https"
  host: string
  port: string
  onScheme: (scheme: "http" | "https") => void
  onHost: (host: string) => void
  onPort: (port: string) => void
  showScheme?: boolean
}) {
  return (
    <div className={`grid gap-3 ${showScheme ? "grid-cols-[110px_1fr_110px]" : "grid-cols-[1fr_110px]"}`}>
      {showScheme && (
        <span className="flex flex-col gap-1.5">
          <Label htmlFor={`${idPrefix}-scheme`}>Scheme</Label>
          <NativeSelect id={`${idPrefix}-scheme`} value={scheme} onChange={(e) => onScheme(e.target.value as "http" | "https")}>
            <option value="http">http</option>
            <option value="https">https</option>
          </NativeSelect>
        </span>
      )}
      <span className="flex flex-col gap-1.5">
        <Label htmlFor={`${idPrefix}-host`}>Host</Label>
        <Input
          id={`${idPrefix}-host`}
          placeholder="localhost"
          value={host}
          onChange={(e) => onHost(e.target.value)}
          onPaste={(e) => {
            // Only fan out when the paste replaces the whole field — a paste into the
            // middle of an existing host (or a partial selection) is a literal insert,
            // not a full address to parse. An empty field trivially qualifies.
            const el = e.currentTarget
            const replacingAll = el.selectionStart === 0 && el.selectionEnd === el.value.length
            if (!replacingAll) return

            const text = e.clipboardData.getData("text").trim()
            const pasted = parsePastedURL(text)
            if (pasted) {
              e.preventDefault()
              onScheme(pasted.scheme)
              onHost(pasted.host)
              onPort(pasted.port)
              return
            }
            // A bare "host:port" (Deluge, or any kind's Host typed without a scheme) —
            // route the port to the Port field instead of leaving it stuck in Host,
            // where it would double up against the kind's seeded port default.
            const hp = decomposeHostPort(text)
            if (hp.port && hp.host) {
              e.preventDefault()
              onHost(hp.host)
              onPort(hp.port)
            }
          }}
        />
      </span>
      <span className="flex flex-col gap-1.5">
        <Label htmlFor={`${idPrefix}-port`}>Port</Label>
        <Input
          id={`${idPrefix}-port`}
          type="number"
          min={1}
          max={65535}
          placeholder="default"
          value={port}
          onChange={(e) => onPort(e.target.value)}
        />
      </span>
    </div>
  )
}
