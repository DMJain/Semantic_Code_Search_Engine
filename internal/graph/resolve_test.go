package graph

import (
	"testing"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/parse"
)

// TestAnalyzeKnownReceiverCrossPackage proves Rule 3 through an import alias: a
// parameter written `d *dep.T` resolves `d.M()` to the method T.M in the aliased
// indexed package, exact concrete_method, and an interface-typed parameter is
// counted deferred_interface with no stored row.
func TestAnalyzeKnownReceiverCrossPackage(t *testing.T) {
	src := []byte(`package p

import dep "example.com/dep"

func F(d *dep.T, i Iface) {
	d.M()
	i.N()
}
`)
	info, err := Extract(src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	syms := []Symbol{
		{ID: 1, Path: "p/p.go", Kind: parse.KindFunc, Package: "example.com/p",
			Name: "F", QualifiedName: "example.com/p.F", StartLine: 5},
		{ID: 2, Path: "dep/dep.go", Kind: parse.KindMethod, Package: "example.com/dep",
			Name: "M", QualifiedName: "example.com/dep.(T).M", Receiver: "T", StartLine: 1},
		{ID: 3, Path: "p/p.go", Kind: parse.KindInterface, Package: "example.com/p",
			Name: "Iface", QualifiedName: "example.com/p.Iface", StartLine: 12},
	}
	ix := NewIndex(syms)
	names := NewNameIndex()
	names.Add("example.com/p", map[string]string{"F": "func", "Iface": "type"})
	names.Add("example.com/dep", map[string]string{"T": "type"})

	edges, st := Analyze("p/p.go", "example.com/p", info, ix, names)
	want := []Edge{{
		CallerID: 1, CalleeID: 2, CalleeRaw: "d.M", Resolution: ResolutionExact,
		CallKind: KindConcreteMethod, CallLine: 6,
	}}
	if len(edges) != 1 || edges[0] != want[0] {
		t.Fatalf("edges = %+v, want %+v", edges, want)
	}
	if st.Exact != 1 || st.DeferredInterface != 1 || st.DeferredSelector != 0 {
		t.Errorf("stats exact=%d deferred_interface=%d deferred_selector=%d, want 1/1/0",
			st.Exact, st.DeferredInterface, st.DeferredSelector)
	}
	if err := st.Check(); err != nil {
		t.Errorf("invariant: %v", err)
	}
}

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

// TestAnalyzeTypeParameterNotGuessed proves a receiver or parameter typed as the
// declaration's own type parameter is unknown even when the package declares a
// type of the same name with that method, so no wrong concrete_method edge is
// stored (spec story 40: unknown types are never guessed).
func TestAnalyzeTypeParameterNotGuessed(t *testing.T) {
	src := []byte(`package p

type T struct{}

func (T) M() {}

func F[T any](x T) {
	x.M()
}
`)
	info, err := Extract(src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	ix := NewIndex([]Symbol{
		{ID: 1, Path: "p/p.go", Kind: parse.KindFunc, Package: "example.com/p",
			Name: "F", QualifiedName: "example.com/p.F", StartLine: 7},
		{ID: 2, Path: "p/p.go", Kind: parse.KindMethod, Package: "example.com/p",
			Name: "M", QualifiedName: "example.com/p.(T).M", Receiver: "T", StartLine: 5},
	})
	names := NewNameIndex()
	names.Add("example.com/p", map[string]string{"F": "func", "T": "type"})

	edges, st := Analyze("p/p.go", "example.com/p", info, ix, names)
	if len(edges) != 0 {
		t.Fatalf("edges = %+v, want none (T is a type parameter)", edges)
	}
	if st.DeferredSelector != 1 || st.Exact != 0 {
		t.Errorf("stats exact=%d deferred_selector=%d, want 0/1", st.Exact, st.DeferredSelector)
	}
	if err := st.Check(); err != nil {
		t.Errorf("invariant: %v", err)
	}
}

// TestAnalyzeBareTypeNotDeclaredNotGuessed proves a bare type name resolves only
// when the package declares a type with that name: a method symbol with a
// receiver base the declared-name index does not know is not guessed.
func TestAnalyzeBareTypeNotDeclaredNotGuessed(t *testing.T) {
	src := []byte(`package p

func F(x *Ghost) {
	x.M()
}
`)
	info, err := Extract(src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	ix := NewIndex([]Symbol{
		{ID: 1, Path: "p/p.go", Kind: parse.KindFunc, Package: "example.com/p",
			Name: "F", QualifiedName: "example.com/p.F", StartLine: 3},
		{ID: 2, Path: "p/p.go", Kind: parse.KindMethod, Package: "example.com/p",
			Name: "M", QualifiedName: "example.com/p.(Ghost).M", Receiver: "Ghost", StartLine: 1},
	})
	// The declared-name index deliberately does not list Ghost.
	names := NewNameIndex()
	names.Add("example.com/p", map[string]string{"F": "func"})

	edges, st := Analyze("p/p.go", "example.com/p", info, ix, names)
	if len(edges) != 0 {
		t.Fatalf("edges = %+v, want none (Ghost is not a declared type)", edges)
	}
	if st.DeferredSelector != 1 || st.Exact != 0 {
		t.Errorf("stats exact=%d deferred_selector=%d, want 0/1", st.Exact, st.DeferredSelector)
	}
}
