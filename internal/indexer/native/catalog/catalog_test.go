package catalog

import "testing"

// TestFamilyIDsUniqueAcrossPackages fails if two driver packages declare the
// same definition id: All() is last-writer-wins by id, so a collision silently
// makes one family unreachable (the usenet/torrent AnimeTosho pair once did).
func TestFamilyIDsUniqueAcrossPackages(t *testing.T) {
	t.Parallel()

	seen := make(map[string]string)
	for _, fams := range families() {
		for _, f := range fams {
			id, name := f.Definition.ID, f.Definition.Name
			if prev, dup := seen[id]; dup {
				t.Errorf("definition id %q declared twice: %q and %q", id, prev, name)
			}
			seen[id] = name
		}
	}
}
