package backup

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/autobrr/harbrr/internal/secrets"
)

// TestDecodeHonorsRecordedKDF proves an importer derives the key from the KDF params
// RECORDED in the bundle, not the compile-time default — so a bundle sealed under a
// non-default cost set still opens. This is the guarantee that keeps old backups
// restorable after a future default-cost bump; it fails against a decode that pins the
// current default.
func TestDecodeHonorsRecordedKDF(t *testing.T) {
	t.Parallel()
	payload, err := json.Marshal(Tables{AppSettings: []AppSettingRow{{Key: "log.level", Value: "debug"}}})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	salt, err := secrets.NewPassphraseSalt()
	if err != nil {
		t.Fatalf("salt: %v", err)
	}
	// A valid but non-default KDF (lower memory than DefaultPassphraseKDF).
	kdf := secrets.PassphraseKDF{Algorithm: "argon2id", Memory: 16 * 1024, Time: 2, Threads: 1}
	if kdf == secrets.DefaultPassphraseKDF() {
		t.Fatal("test KDF must differ from the default to be meaningful")
	}
	key, err := secrets.DeriveKeyFromPassphrase("pw", salt, kdf)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	sealed, err := secrets.EncryptWithKey(key, []byte(payloadAAD), payload)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	env, err := json.Marshal(Envelope{
		SchemaVersion: SchemaVersion, KDF: kdf,
		Salt: base64.StdEncoding.EncodeToString(salt), Payload: sealed,
	})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}

	tables, err := (&Service{}).decode(env, "pw")
	if err != nil {
		t.Fatalf("decode(non-default KDF bundle): %v — importer ignored the recorded KDF", err)
	}
	if len(tables.AppSettings) != 1 || tables.AppSettings[0].Value != "debug" {
		t.Errorf("decoded tables = %+v, want the sealed app setting", tables.AppSettings)
	}
}
