package graph

import "testing"

// TestFingerprintVersionAndInputs proves the freshness fingerprint (D-076) is
// deterministic for equal inputs, changes when the resolver version is bumped
// (so old edges go stale instead of silently reused), changes when a symbol's
// identity changes, and ignores line numbers (the body-only-edit case).
func TestFingerprintVersionAndInputs(t *testing.T) {
	syms := []Symbol{
		{ID: 1, Kind: "func", Package: "p", QualifiedName: "p.F", Receiver: "", StartLine: 3},
		{ID: 2, Kind: "method", Package: "p", QualifiedName: "p.T.M", Receiver: "*T", StartLine: 7},
	}

	base := Fingerprint(syms, ResolverVersion)
	if base != Fingerprint(syms, ResolverVersion) {
		t.Fatal("fingerprint is not deterministic for equal inputs")
	}
	if base == Fingerprint(syms, ResolverVersion+1) {
		t.Error("fingerprint unchanged when the resolver version is bumped")
	}

	moved := append([]Symbol(nil), syms...)
	moved[0].StartLine = 99
	if base != Fingerprint(moved, ResolverVersion) {
		t.Error("fingerprint changed on a line-number-only change")
	}

	rekinded := append([]Symbol(nil), syms...)
	rekinded[0].Kind = "method"
	if base == Fingerprint(rekinded, ResolverVersion) {
		t.Error("fingerprint unchanged when a symbol's kind changed")
	}

	added := append(append([]Symbol(nil), syms...), Symbol{ID: 3, Kind: "var", Package: "p", QualifiedName: "p.V"})
	if base == Fingerprint(added, ResolverVersion) {
		t.Error("fingerprint unchanged when a symbol is added")
	}
}
