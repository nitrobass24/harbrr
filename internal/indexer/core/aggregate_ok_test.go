package core

import "testing"

// TestMemberOutcomeZeroValueIsNotOK pins the OK() boundary invariant: a hand-built or
// zero outcome with no Indexer must read as skipped, not searchable — Provider is an
// exported interface and the fan-out nil-derefs anything OK() admits.
func TestMemberOutcomeZeroValueIsNotOK(t *testing.T) {
	t.Parallel()
	if (MemberOutcome{}).OK() {
		t.Fatal("zero MemberOutcome must not read OK (nil Indexer would reach the fan-out)")
	}
}
