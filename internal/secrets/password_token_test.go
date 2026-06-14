package secrets_test

import (
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/secrets"
)

func TestHashVerifyPassword(t *testing.T) {
	t.Parallel()

	const pw = "correct horse battery staple"
	hash, err := secrets.HashPassword(pw)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=65536,t=3,p=2$") {
		t.Errorf("PHC prefix wrong: %q", hash)
	}
	if strings.Contains(hash, pw) {
		t.Error("hash contains the plaintext password")
	}

	ok, err := secrets.VerifyPassword(pw, hash)
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if !ok {
		t.Error("correct password did not verify")
	}

	bad, err := secrets.VerifyPassword("wrong", hash)
	if err != nil {
		t.Fatalf("VerifyPassword(wrong): %v", err)
	}
	if bad {
		t.Error("wrong password verified")
	}
}

func TestHashPasswordSaltedDistinct(t *testing.T) {
	t.Parallel()

	a, err := secrets.HashPassword("same")
	if err != nil {
		t.Fatalf("hash a: %v", err)
	}
	b, err := secrets.HashPassword("same")
	if err != nil {
		t.Fatalf("hash b: %v", err)
	}
	if a == b {
		t.Error("two hashes of the same password are identical — salt not random")
	}
}

func TestVerifyPasswordMalformed(t *testing.T) {
	t.Parallel()

	for _, enc := range []string{"", "not-phc", "$argon2id$v=19$bad$salt$hash"} {
		if _, err := secrets.VerifyPassword("x", enc); err == nil {
			t.Errorf("VerifyPassword(%q) returned no error", enc)
		}
	}
}

func TestTokenGenerateHashVerify(t *testing.T) {
	t.Parallel()

	key, err := secrets.GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}
	hash := secrets.HashToken(key)

	if hash == key {
		t.Error("stored hash equals the plaintext token")
	}
	if len(hash) != 64 { // SHA-256 hex
		t.Errorf("hash len = %d, want 64", len(hash))
	}
	if !secrets.VerifyToken(key, hash) {
		t.Error("correct token did not verify")
	}
	if secrets.VerifyToken("other", hash) {
		t.Error("wrong token verified")
	}
}

func TestGenerateAPIKeyUnique(t *testing.T) {
	t.Parallel()

	seen := map[string]struct{}{}
	for range 100 {
		k, err := secrets.GenerateAPIKey()
		if err != nil {
			t.Fatalf("GenerateAPIKey: %v", err)
		}
		if _, dup := seen[k]; dup {
			t.Fatal("duplicate API key generated")
		}
		seen[k] = struct{}{}
	}
}

func TestRedactedSentinel(t *testing.T) {
	t.Parallel()

	if !secrets.IsRedacted(secrets.Redacted) {
		t.Error("IsRedacted(Redacted) = false")
	}
	if secrets.IsRedacted("") || secrets.IsRedacted("real-value") {
		t.Error("IsRedacted matched a non-sentinel value")
	}
}
