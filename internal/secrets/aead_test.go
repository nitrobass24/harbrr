package secrets

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

// synthetic test key + plaintext (synthetic secrets live only in *_test.go).
var (
	testKey  = bytes.Repeat([]byte{0x42}, keyLen)
	testKey2 = bytes.Repeat([]byte{0x17}, keyLen)
)

func TestAADVector(t *testing.T) {
	t.Parallel()

	// Pins the documented AAD encoding: "<id>\x00<setting>".
	got := aad(42, "apikey")
	want := []byte{0x34, 0x32, 0x00, 0x61, 0x70, 0x69, 0x6b, 0x65, 0x79} // "42\0apikey"
	if !bytes.Equal(got, want) {
		t.Errorf("aad(42,apikey) = % x, want % x", got, want)
	}
}

func TestSealOpenRoundTrip(t *testing.T) {
	t.Parallel()

	ad := aad(1, "passkey")
	blob, err := seal(testKey, ad, []byte("super-secret-passkey"))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	got, err := open(testKey, ad, blob)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if string(got) != "super-secret-passkey" {
		t.Errorf("round-trip = %q, want the original plaintext", got)
	}
}

func TestSealBlobNeverContainsPlaintext(t *testing.T) {
	t.Parallel()

	plaintext := "DISTINCTIVE-PLAINTEXT-MARKER-9173"
	blob, err := seal(testKey, aad(1, "x"), []byte(plaintext))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if strings.Contains(blob, plaintext) {
		t.Error("base64 blob contains the plaintext")
	}
	raw, err := base64.StdEncoding.DecodeString(blob)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if bytes.Contains(raw, []byte(plaintext)) {
		t.Error("ciphertext bytes contain the plaintext")
	}
}

func TestSealTamperFails(t *testing.T) {
	t.Parallel()

	ad := aad(1, "x")
	blob, err := seal(testKey, ad, []byte("payload"))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	raw, _ := base64.StdEncoding.DecodeString(blob)
	raw[len(raw)-1] ^= 0x01 // flip one bit of the tag
	tampered := base64.StdEncoding.EncodeToString(raw)

	if _, err := open(testKey, ad, tampered); err == nil {
		t.Error("open accepted a tampered ciphertext, want auth failure")
	}
}

func TestOpenWrongKeyFails(t *testing.T) {
	t.Parallel()

	ad := aad(1, "x")
	blob, err := seal(testKey, ad, []byte("payload"))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if _, err := open(testKey2, ad, blob); err == nil {
		t.Error("open accepted a wrong key, want auth failure")
	}
}

func TestOpenAADMismatchFails(t *testing.T) {
	t.Parallel()

	blob, err := seal(testKey, aad(1, "passkey"), []byte("payload"))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	// Same key, different AAD (other instance or setting) must fail — this is the
	// cross-row replay protection.
	if _, err := open(testKey, aad(2, "passkey"), blob); err == nil {
		t.Error("open accepted a different instance AAD, want auth failure")
	}
	if _, err := open(testKey, aad(1, "cookie"), blob); err == nil {
		t.Error("open accepted a different setting AAD, want auth failure")
	}
}

// TestSealNonceUnique proves seal draws a fresh random nonce each call: sealing the
// same plaintext+AAD many times must never repeat the prepended nonce. A reused
// nonce under a fixed GCM key is catastrophic (it leaks the keystream XOR), so this
// is a standing guard, not a one-off check.
func TestSealNonceUnique(t *testing.T) {
	t.Parallel()

	const (
		nonceLen = 12 // AES-GCM standard nonce size.
		iters    = 2048
	)
	ad := aad(1, "passkey")
	seen := make(map[string]struct{}, iters)
	for i := 0; i < iters; i++ {
		blob, err := seal(testKey, ad, []byte("same-plaintext-every-time"))
		if err != nil {
			t.Fatalf("seal[%d]: %v", i, err)
		}
		raw, err := base64.StdEncoding.DecodeString(blob)
		if err != nil {
			t.Fatalf("decode[%d]: %v", i, err)
		}
		if len(raw) < nonceLen {
			t.Fatalf("blob[%d] shorter than the nonce", i)
		}
		nonce := string(raw[:nonceLen])
		if _, dup := seen[nonce]; dup {
			t.Fatalf("nonce repeated after %d seals", i)
		}
		seen[nonce] = struct{}{}
	}
}

func TestOpenErrorHasNoPlaintext(t *testing.T) {
	t.Parallel()

	plaintext := "LEAK-CANARY-55512"
	blob, err := seal(testKey, aad(1, "x"), []byte(plaintext))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	_, err = open(testKey2, aad(1, "x"), blob)
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), plaintext) {
		t.Errorf("error message leaked the plaintext: %v", err)
	}
}
