package graph

import "testing"

// TestFingerprintVersionAndInputs proves the freshness fingerprint (D-076,
// amended to cover declared names) is deterministic for equal inputs, changes
// when the resolver version is bumped (so old edges go stale instead of silently
// reused), changes when a symbol's identity changes or a declared name changes,
// and ignores line numbers (the body-only-edit case).
func TestFingerprintVersionAndInputs(t *testing.T) {
	syms := []Symbol{
		{ID: 1, Kind: "func", Package: "p", QualifiedName: "p.F", Receiver: "", StartLine: 3},
		{ID: 2, Kind: "method", Package: "p", QualifiedName: "p.T.M", Receiver: "*T", StartLine: 7},
	}
	names := NewNameIndex()
	names.Add("p", map[string]string{"F": "func", "Foo": "type"})
	names.Add("q", nil)

	base := Fingerprint(syms, names, ResolverVersion)
	if base != Fingerprint(syms, names, ResolverVersion) {
		t.Fatal("fingerprint is not deterministic for equal inputs")
	}
	if base == Fingerprint(syms, names, ResolverVersion+1) {
		t.Error("fingerprint unchanged when the resolver version is bumped")
	}

	moved := append([]Symbol(nil), syms...)
	moved[0].StartLine = 99
	if base != Fingerprint(moved, names, ResolverVersion) {
		t.Error("fingerprint changed on a line-number-only change")
	}

	rekinded := append([]Symbol(nil), syms...)
	rekinded[0].Kind = "method"
	if base == Fingerprint(rekinded, names, ResolverVersion) {
		t.Error("fingerprint unchanged when a symbol's kind changed")
	}

	added := append(append([]Symbol(nil), syms...), Symbol{ID: 3, Kind: "var", Package: "p", QualifiedName: "p.V"})
	if base == Fingerprint(added, names, ResolverVersion) {
		t.Error("fingerprint unchanged when a symbol is added")
	}

	// A declared-name-only change (a non-struct type, which is not a symbol) must
	// change the fingerprint: Rule 2 and the conversion classification depend on
	// it.
	withType := NewNameIndex()
	withType.Add("p", map[string]string{"F": "func", "Foo": "type", "Bar": "type"})
	withType.Add("q", nil)
	if base == Fingerprint(syms, withType, ResolverVersion) {
		t.Error("fingerprint unchanged when a declared name is added")
	}

	// The digest is order-independent, so the same names in a different insertion
	// order give the same fingerprint.
	reordered := NewNameIndex()
	reordered.Add("q", nil)
	reordered.Add("p", map[string]string{"Foo": "type", "F": "func"})
	if base != Fingerprint(syms, reordered, ResolverVersion) {
		t.Error("fingerprint changed when the same names were added in a different order")
	}
}
