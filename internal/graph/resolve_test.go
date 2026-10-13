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
	if st.Exact != 1 || st.DeferredInterface != 1 || st.NameOnly != 0 {
		t.Errorf("stats exact=%d deferred_interface=%d name_only=%d, want 1/1/0",
			st.Exact, st.DeferredInterface, st.NameOnly)
	}
	if err := st.Check(); err != nil {
		t.Errorf("invariant: %v", err)
	}
}

// TestFingerprintVersionAndInputs proves the freshness fingerprint (D-076,
// amended to cover declared names, struct field types and constructor results)
// is deterministic for equal inputs, changes when the resolver version is
// bumped (so old edges go stale instead of silently reused), changes when a
// symbol's identity, a declared name, a struct field's type or a constructor
// result changes, and ignores line numbers (the body-only-edit case).
func TestFingerprintVersionAndInputs(t *testing.T) {
	syms := []Symbol{
		{ID: 1, Kind: "func", Package: "p", QualifiedName: "p.F", Receiver: "", StartLine: 3},
		{ID: 2, Kind: "method", Package: "p", QualifiedName: "p.T.M", Receiver: "*T", StartLine: 7},
	}
	names := NewNameIndex()
	names.Add("p", map[string]string{"F": "func", "Foo": "type"})
	names.Add("q", nil)
	names.AddFields("p", map[string]TypeRef{"S\x00cache": {Name: "Cache"}})
	names.AddResults("p", map[string]TypeRef{"NewT": {Name: "T"}})

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
	withType.AddFields("p", map[string]TypeRef{"S\x00cache": {Name: "Cache"}})
	withType.AddResults("p", map[string]TypeRef{"NewT": {Name: "T"}})
	if base == Fingerprint(syms, withType, ResolverVersion) {
		t.Error("fingerprint unchanged when a declared name is added")
	}

	// A struct field's resolved type is a resolution input (Rule 3 field chains),
	// so changing it must change the fingerprint.
	changedField := NewNameIndex()
	changedField.Add("p", map[string]string{"F": "func", "Foo": "type"})
	changedField.Add("q", nil)
	changedField.AddFields("p", map[string]TypeRef{"S\x00cache": {Name: "Other"}})
	changedField.AddResults("p", map[string]TypeRef{"NewT": {Name: "T"}})
	if base == Fingerprint(syms, changedField, ResolverVersion) {
		t.Error("fingerprint unchanged when a struct field type changed")
	}

	// A constructor result is a resolution input (`x := NewT(...)`), so changing
	// it must change the fingerprint.
	changedResult := NewNameIndex()
	changedResult.Add("p", map[string]string{"F": "func", "Foo": "type"})
	changedResult.Add("q", nil)
	changedResult.AddFields("p", map[string]TypeRef{"S\x00cache": {Name: "Cache"}})
	changedResult.AddResults("p", map[string]TypeRef{"NewT": {Name: "Other"}})
	if base == Fingerprint(syms, changedResult, ResolverVersion) {
		t.Error("fingerprint unchanged when a constructor result changed")
	}

	// The digest is order-independent, so the same names in a different insertion
	// order give the same fingerprint.
	reordered := NewNameIndex()
	reordered.Add("q", nil)
	reordered.Add("p", map[string]string{"Foo": "type", "F": "func"})
	reordered.AddResults("p", map[string]TypeRef{"NewT": {Name: "T"}})
	reordered.AddFields("p", map[string]TypeRef{"S\x00cache": {Name: "Cache"}})
	if base != Fingerprint(syms, reordered, ResolverVersion) {
		t.Error("fingerprint changed when the same names were added in a different order")
	}
}

// TestAnalyzeTypeParameterNotConcreteMethod proves a receiver or parameter typed
// as the declaration's own type parameter is never resolved to a concrete
// method of a same-named package type. It is an unknown receiver, so Rule 5
// stores an honest name_only guess with the one same-package method, never a
// wrong exact (spec story 40: unknown types are never guessed).
func TestAnalyzeTypeParameterNotConcreteMethod(t *testing.T) {
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
	want := []Edge{{
		CallerID: 1, CalleeID: 2, CalleeRaw: "x.M", Resolution: ResolutionNameOnly,
		CallKind: KindUnknown, CallLine: 8,
	}}
	if len(edges) != 1 || edges[0] != want[0] {
		t.Fatalf("edges = %+v, want %+v", edges, want)
	}
	if st.NameOnly != 1 || st.Exact != 0 {
		t.Errorf("stats exact=%d name_only=%d, want 0/1", st.Exact, st.NameOnly)
	}
	if err := st.Check(); err != nil {
		t.Errorf("invariant: %v", err)
	}
}

// TestAnalyzeBareTypeNotDeclaredRule5 proves a bare type name that the package
// does not declare is not resolved to a concrete method; it is an unknown
// receiver, so Rule 5 stores a name_only guess when exactly one same-package
// method matches and never a wrong exact.
func TestAnalyzeBareTypeNotDeclaredRule5(t *testing.T) {
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
	if len(edges) != 1 || edges[0].Resolution != ResolutionNameOnly || edges[0].CalleeID != 2 {
		t.Fatalf("edges = %+v, want one name_only to Ghost.M", edges)
	}
	if st.NameOnly != 1 || st.Exact != 0 {
		t.Errorf("stats exact=%d name_only=%d, want 0/1", st.Exact, st.NameOnly)
	}
}

// TestAnalyzeConstructorAndFieldChain proves Rule 3 through the package facts:
// `x := NewT()` with a single-result same-package constructor gives x that type,
// and `s.cache.Assume()` resolves the struct field chain to Cache and its method.
func TestAnalyzeConstructorAndFieldChain(t *testing.T) {
	src := []byte(`package p

type T struct{}
type Cache struct{}
type Scheduler struct{ cache *Cache }

func (T) M() {}
func (c *Cache) Assume() {}
func NewT() T { return T{} }

func F(s *Scheduler) {
	x := NewT()
	x.M()
	s.cache.Assume()
}
`)
	info, err := Extract(src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	ix := NewIndex([]Symbol{
		{ID: 1, Path: "p/p.go", Kind: parse.KindFunc, Package: "example.com/p",
			Name: "F", QualifiedName: "example.com/p.F", StartLine: 11},
		{ID: 2, Path: "p/p.go", Kind: parse.KindMethod, Package: "example.com/p",
			Name: "M", QualifiedName: "example.com/p.(T).M", Receiver: "T", StartLine: 7},
		{ID: 3, Path: "p/p.go", Kind: parse.KindMethod, Package: "example.com/p",
			Name: "Assume", QualifiedName: "example.com/p.(*Cache).Assume", Receiver: "*Cache", StartLine: 8},
		{ID: 4, Path: "p/p.go", Kind: parse.KindFunc, Package: "example.com/p",
			Name: "NewT", QualifiedName: "example.com/p.NewT", StartLine: 9},
	})
	names := NewNameIndex()
	names.Add("example.com/p", map[string]string{"F": "func", "T": "type", "Cache": "type", "Scheduler": "type", "NewT": "func"})
	names.AddFields("example.com/p", map[string]TypeRef{"Scheduler\x00cache": {Name: "Cache"}})
	names.AddResults("example.com/p", map[string]TypeRef{"NewT": {Name: "T"}})

	edges, st := Analyze("p/p.go", "example.com/p", info, ix, names)
	want := []Edge{
		{CallerID: 1, CalleeID: 4, CalleeRaw: "NewT", Resolution: ResolutionExact,
			CallKind: KindFunc, CallLine: 12},
		{CallerID: 1, CalleeID: 2, CalleeRaw: "x.M", Resolution: ResolutionExact,
			CallKind: KindConcreteMethod, CallLine: 13},
		{CallerID: 1, CalleeID: 3, CalleeRaw: "s.cache.Assume", Resolution: ResolutionExact,
			CallKind: KindConcreteMethod, CallLine: 14},
	}
	if len(edges) != len(want) {
		t.Fatalf("edges = %+v, want %+v", edges, want)
	}
	for i := range want {
		if edges[i] != want[i] {
			t.Errorf("edge %d = %+v, want %+v", i, edges[i], want[i])
		}
	}
	if st.Exact != 3 || st.NameOnly != 0 || st.Unresolved != 0 {
		t.Errorf("stats exact=%d name_only=%d unresolved=%d, want 3/0/0", st.Exact, st.NameOnly, st.Unresolved)
	}
	if err := st.Check(); err != nil {
		t.Errorf("invariant: %v", err)
	}
}

// TestAnalyzeRule5 proves Rule 5 (spec story 56): an unknown receiver is looked
// up by method name in the caller's package only: one method is name_only with
// that callee, two methods are two ambiguous rows with identical raw text, and
// none is unresolved with reason unknown_receiver.
func TestAnalyzeRule5(t *testing.T) {
	src := []byte(`package p

type A struct{}
type B struct{}
type C struct{}

func (A) Only() {}
func (B) Two()  {}
func (C) Two()  {}

func F(y interface{}) {
	y.Only()
	y.Two()
	y.Missing()
}
`)
	info, err := Extract(src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	ix := NewIndex([]Symbol{
		{ID: 1, Path: "p/p.go", Kind: parse.KindFunc, Package: "example.com/p",
			Name: "F", QualifiedName: "example.com/p.F", StartLine: 11},
		{ID: 2, Path: "p/p.go", Kind: parse.KindMethod, Package: "example.com/p",
			Name: "Only", QualifiedName: "example.com/p.(A).Only", Receiver: "A"},
		{ID: 3, Path: "p/p.go", Kind: parse.KindMethod, Package: "example.com/p",
			Name: "Two", QualifiedName: "example.com/p.(B).Two", Receiver: "B"},
		{ID: 4, Path: "p/p.go", Kind: parse.KindMethod, Package: "example.com/p",
			Name: "Two", QualifiedName: "example.com/p.(C).Two", Receiver: "C"},
	})
	names := NewNameIndex()
	names.Add("example.com/p", map[string]string{"F": "func", "A": "type", "B": "type", "C": "type"})

	edges, st := Analyze("p/p.go", "example.com/p", info, ix, names)
	want := []Edge{
		{CallerID: 1, CalleeID: 2, CalleeRaw: "y.Only", Resolution: ResolutionNameOnly,
			CallKind: KindUnknown, CallLine: 12},
		{CallerID: 1, CalleeID: 3, CalleeRaw: "y.Two", Resolution: ResolutionAmbiguous,
			CallKind: KindUnknown, CallLine: 13},
		{CallerID: 1, CalleeID: 4, CalleeRaw: "y.Two", Resolution: ResolutionAmbiguous,
			CallKind: KindUnknown, CallLine: 13},
		{CallerID: 1, CalleeID: 0, CalleeRaw: "y.Missing", Resolution: ResolutionUnresolved,
			CallKind: KindUnknown, CallLine: 14, UnresolvedReason: ReasonUnknownReceiver},
	}
	if len(edges) != len(want) {
		t.Fatalf("edges = %+v, want %+v", edges, want)
	}
	for i := range want {
		if edges[i] != want[i] {
			t.Errorf("edge %d = %+v, want %+v", i, edges[i], want[i])
		}
	}
	if st.NameOnly != 1 || st.Ambiguous != 2 || st.Unresolved != 1 {
		t.Errorf("stats name_only=%d ambiguous=%d unresolved=%d, want 1/2/1",
			st.NameOnly, st.Ambiguous, st.Unresolved)
	}
	if st.Reasons[ReasonUnknownReceiver] != 1 {
		t.Errorf("unknown_receiver reasons = %d, want 1", st.Reasons[ReasonUnknownReceiver])
	}
	if err := st.Check(); err != nil {
		t.Errorf("invariant: %v", err)
	}
}

// TestAnalyzeFieldTypeUsesDeclaringAlias proves a struct field's qualified type
// is resolved against the DECLARING file's alias and stored as an import path,
// so a caller file that binds the same alias to a different package cannot
// redirect it: `s.queue.Only()` resolves to the declaring package's Queue.Only,
// never the colliding same-named type in the caller's other package.
func TestAnalyzeFieldTypeUsesDeclaringAlias(t *testing.T) {
	src := []byte(`package p

import foo "example.com/other"

type S struct{}

func F(s *S) {
	s.queue.Only()
}
`)
	info, err := Extract(src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	ix := NewIndex([]Symbol{
		{ID: 1, Path: "p/p.go", Kind: parse.KindFunc, Package: "example.com/p",
			Name: "F", QualifiedName: "example.com/p.F", StartLine: 7},
		{ID: 2, Path: "q/q.go", Kind: parse.KindMethod, Package: "example.com/q",
			Name: "Only", QualifiedName: "example.com/q.(Queue).Only", Receiver: "Queue"},
		{ID: 3, Path: "other/other.go", Kind: parse.KindMethod, Package: "example.com/other",
			Name: "Only", QualifiedName: "example.com/other.(Queue).Only", Receiver: "Queue"},
	})
	names := NewNameIndex()
	names.Add("example.com/p", map[string]string{"F": "func", "S": "type"})
	names.Add("example.com/q", map[string]string{"Queue": "type"})
	names.Add("example.com/other", map[string]string{"Queue": "type"})
	// The field's type was resolved in the declaring file, where foo bound q.
	names.AddFields("example.com/p", map[string]TypeRef{"S\x00queue": {Pkg: "example.com/q", Name: "Queue"}})

	edges, st := Analyze("p/p.go", "example.com/p", info, ix, names)
	want := Edge{CallerID: 1, CalleeID: 2, CalleeRaw: "s.queue.Only",
		Resolution: ResolutionExact, CallKind: KindConcreteMethod, CallLine: 8}
	if len(edges) != 1 || edges[0] != want {
		t.Fatalf("edges = %+v, want [%+v] (must not resolve to the other package)", edges, want)
	}
	if st.Exact != 1 {
		t.Errorf("exact = %d, want 1", st.Exact)
	}
}

// TestAnalyzeInterfaceFieldFallsToDeferred proves a same-package interface field
// whose qualified type came from another file resolves to the declared interface
// and is counted deferred_interface, not unknown_receiver: the field's package
// path is carried from the declaring file, so the caller's alias map is
// irrelevant.
func TestAnalyzeInterfaceFieldFallsToDeferred(t *testing.T) {
	src := []byte(`package p

import foo "example.com/other"

type S struct{}

func F(s *S) {
	s.queue.Add()
}
`)
	info, err := Extract(src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	ix := NewIndex([]Symbol{
		{ID: 1, Path: "p/p.go", Kind: parse.KindFunc, Package: "example.com/p",
			Name: "F", QualifiedName: "example.com/p.F", StartLine: 7},
		{ID: 2, Path: "q/q.go", Kind: parse.KindInterface, Package: "example.com/q",
			Name: "Queue", QualifiedName: "example.com/q.Queue"},
	})
	names := NewNameIndex()
	names.Add("example.com/p", map[string]string{"F": "func", "S": "type"})
	names.Add("example.com/q", map[string]string{"Queue": "type"})
	names.AddFields("example.com/p", map[string]TypeRef{"S\x00queue": {Pkg: "example.com/q", Name: "Queue"}})

	edges, st := Analyze("p/p.go", "example.com/p", info, ix, names)
	if len(edges) != 0 {
		t.Fatalf("edges = %+v, want none (interface receiver)", edges)
	}
	if st.DeferredInterface != 1 || st.Unresolved != 0 {
		t.Errorf("deferred_interface = %d, unresolved = %d, want 1/0", st.DeferredInterface, st.Unresolved)
	}
}

// TestAnalyzeConstructorShadowedByLocal proves a local variable that shadows the
// constructor name stops `x := NewT()` from being typed: the call is a local
// function value (deferred_local) and x stays unknown, so x.M is a Rule 5 guess,
// never a wrong exact.
func TestAnalyzeConstructorShadowedByLocal(t *testing.T) {
	src := []byte(`package p

type T struct{}

func (T) M() {}

func NewT() T { return T{} }

func F() {
	NewT := func() T { return T{} }
	x := NewT()
	x.M()
	_ = NewT
}
`)
	info, err := Extract(src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	ix := NewIndex([]Symbol{
		{ID: 1, Path: "p/p.go", Kind: parse.KindFunc, Package: "example.com/p",
			Name: "F", QualifiedName: "example.com/p.F", StartLine: 9},
		{ID: 2, Path: "p/p.go", Kind: parse.KindMethod, Package: "example.com/p",
			Name: "M", QualifiedName: "example.com/p.(T).M", Receiver: "T"},
		{ID: 3, Path: "p/p.go", Kind: parse.KindFunc, Package: "example.com/p",
			Name: "NewT", QualifiedName: "example.com/p.NewT"},
	})
	names := NewNameIndex()
	names.Add("example.com/p", map[string]string{"F": "func", "T": "type", "NewT": "func"})
	names.AddResults("example.com/p", map[string]TypeRef{"NewT": {Name: "T"}})

	edges, st := Analyze("p/p.go", "example.com/p", info, ix, names)
	want := []Edge{{CallerID: 1, CalleeID: 2, CalleeRaw: "x.M",
		Resolution: ResolutionNameOnly, CallKind: KindUnknown, CallLine: 12}}
	if len(edges) != 1 || edges[0] != want[0] {
		t.Fatalf("edges = %+v, want [%+v] (x must stay unknown)", edges, want)
	}
	if st.NameOnly != 1 || st.Exact != 0 || st.DeferredLocal != 1 {
		t.Errorf("name_only=%d exact=%d deferred_local=%d, want 1/0/1",
			st.NameOnly, st.Exact, st.DeferredLocal)
	}
	if err := st.Check(); err != nil {
		t.Errorf("invariant: %v", err)
	}
}
