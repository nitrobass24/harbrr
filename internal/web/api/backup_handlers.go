package api

import (
	"context"
	"encoding/base64"
	"net/http"

	"github.com/autobrr/harbrr/internal/backup"
)

// maxBackupBytes caps an /api/import body. A config bundle is KB-to-low-MB scale even
// for a large instance; 32 MiB is a generous ceiling that still bounds a memory-
// exhaustion POST (the default 1 MiB decodeJSON cap is too tight for a base64 bundle).
const maxBackupBytes = 32 << 20

// exportBackup returns a passphrase-encrypted bundle of harbrr's config + database. The
// response body IS the bundle (a JSON envelope: cleartext metadata + a sealed payload),
// offered as a file download. The passphrase is required and never echoed.
func (rt *router) exportBackup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Passphrase string `json:"passphrase"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	bundle, err := rt.Backup.Export(r.Context(), backup.ExportParams{Passphrase: req.Passphrase})
	if err != nil {
		rt.writeServiceError(w, "export backup", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="harbrr-backup.json"`)
	// The bundle is an encrypted secret; keep it out of any shared/proxy cache.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(bundle)
}

// importBackup restores a bundle. The body is {payload (base64 of the exported bundle),
// passphrase, force}; force is required to overwrite an already-configured instance. A
// wrong passphrase or a malformed bundle is a 400, a non-empty target without force a 409.
func (rt *router) importBackup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Payload    string `json:"payload"`
		Passphrase string `json:"passphrase"`
		Force      bool   `json:"force"`
	}
	if !decodeJSONLimit(w, r, &req, maxBackupBytes) {
		return
	}
	payload, err := base64.StdEncoding.DecodeString(req.Payload)
	if err != nil {
		writeError(w, http.StatusBadRequest, "payload is not valid base64")
		return
	}
	// Capture the pre-restore ids best-effort: a failed listing must not block the
	// restore (the ids are only an eviction nicety — InvalidateAll below is the
	// load-bearing part).
	insts, _ := rt.Registry.List(r.Context())
	if err := rt.Backup.Import(r.Context(), backup.ImportParams{
		Payload: payload, Passphrase: req.Passphrase, Force: req.Force,
	}); err != nil {
		rt.writeServiceError(w, "import backup", err)
		return
	}
	// A restore wipes and re-inserts every indexer instance under a NEW id, but the
	// resolver still maps each slug to an adapter bound to the pre-restore config and
	// a now-deleted id: without this, restored slugs keep serving stale engines and
	// every write-back FK-fails until process restart.
	rt.Registry.InvalidateAll()
	ids := make([]int64, 0, len(insts))
	for _, inst := range insts {
		ids = append(ids, inst.ID)
	}
	rt.Registry.ForgetInstances(r.Context(), ids...)
	rt.reseedAppSettings(r.Context())
	w.WriteHeader(http.StatusNoContent)
}

// reseedAppSettings re-runs the boot-time seeding of every in-memory dial that is
// loaded from app_settings, because a restore wipes and re-inserts that whole table
// (backup/restore.go). Without it the DB holds the bundle's values while the process
// keeps the pre-restore ones: GET /api/config/adult-categories and
// /api/config/rate-limit report the stale value, every engine InvalidateAll rebuilds
// paces at the stale rate default, and the disagreement resolves itself silently on
// the next restart (autobrr/harbrr#650).
//
// Each call is the same loader app.New runs at boot, and each is non-fatal on its own
// terms — the restore itself already succeeded, so a re-seed failure must not turn a
// completed import into an error. A nil Cache/AdultCategories means the dial is
// unwired, which is the same "nothing to re-seed" as a missing row.
func (rt *router) reseedAppSettings(ctx context.Context) {
	rt.Registry.LoadRateDefaultOverride(ctx)
	rt.AdultCategories.LoadPersisted(ctx, rt.Logger)
	if rt.Cache != nil {
		if err := rt.Cache.LoadOverrides(ctx); err != nil {
			rt.Logger.Warn().Err(err).Msg("reloading cache config after restore failed; the running cache keeps its pre-restore tuning until restart")
		}
	}
	if rt.ReloadLogLevel != nil {
		rt.ReloadLogLevel(ctx)
	}
}
