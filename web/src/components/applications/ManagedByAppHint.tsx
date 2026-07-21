import { Link } from "@tanstack/react-router"
import { useApps } from "@/hooks/useApps"

// Shown in an edit dialog in place of the identity/credential fields (base URL, API
// key, harbrr URL): those now live on the App (ADR 0004) and rotate via AppsSection's
// own edit dialog on the Settings page, not this surface's PATCH. Renders nothing
// when appId is absent (a host-less download client, or a pre-migration row with no
// App yet) — there is no app to point at. The link navigates to Settings, which
// unmounts the page owning this dialog, so no explicit dialog-close plumbing is needed.
export function ManagedByAppHint({ appId }: { appId: number | null | undefined }) {
  const apps = useApps()
  if (appId == null) return null
  const app = apps.data?.find((a) => a.id === appId)
  return (
    <p className="text-[12px] text-faint">
      Identity and credential are managed by app{" "}
      <Link to="/settings" hash="apps-section" className="font-medium text-foreground underline underline-offset-2">
        {app?.name ?? `#${appId}`}
      </Link>
      .
    </p>
  )
}
