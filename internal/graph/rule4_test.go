package graph

import (
	"reflect"
	"testing"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/parse"
)

// candidateFixture builds an index and interface facts from a source file.
func candidateFixture(t *testing.T, src string, syms []Symbol) (*Index, *NameIndex) {
	t.Helper()
	facts, err := SourceFacts([]byte(src))
	if err != nil {
		t.Fatalf("SourceFacts: %v", err)
	}
	names := NewNameIndex()
	names.Add("p", facts.Declared)
	names.AddInterfaces("p", facts.Interfaces)
	return NewIndex(syms), names
}

// TestInterfaceCandidatesBasic proves Rule 4 candidates are concrete indexed
// types that declare every method name of the interface, with value and pointer
// receivers both counting: interface{Get;Set} with A{Get,Set} and B{Get} gives
// only A.
func TestInterfaceCandidatesBasic(t *testing.T) {
	src := `package p

type Store interface {
	Get() int
	Set(int)
}

type A struct{}

func (A) Get() int { return 0 }
func (*A) Set(int) {}

type B struct{}

func (B) Get() int { return 0 }
`
	ix, names := candidateFixture(t, src, []Symbol{
		{ID: 1, Kind: parse.KindMethod, Package: "p", Name: "Get", Receiver: "A"},
		{ID: 2, Kind: parse.KindMethod, Package: "p", Name: "Set", Receiver: "*A"},
		{ID: 3, Kind: parse.KindMethod, Package: "p", Name: "Get", Receiver: "B"},
	})
	got, partial, capped := ix.InterfaceCandidates(names, "p", "Store", 0, UncappedCandidates)
	want := []TypeKey{{Pkg: "p", Type: "A"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("candidates = %+v, want %+v", got, want)
	}
	if partial || capped {
		t.Errorf("partial=%v capped=%v, want false/false", partial, capped)
	}
}

// TestInterfaceCandidatesEmbedded proves a same-package embedded interface is
// expanded recursively, so a type implementing the embedded methods qualifies.
func TestInterfaceCandidatesEmbedded(t *testing.T) {
	src := `package p

type Base interface {
	Get() int
}

type Both interface {
	Base
	Set(int)
}

type C struct{}

func (C) Get() int { return 0 }
func (C) Set(int)   {}
`
	ix, names := candidateFixture(t, src, []Symbol{
		{ID: 1, Kind: parse.KindMethod, Package: "p", Name: "Get", Receiver: "C"},
		{ID: 2, Kind: parse.KindMethod, Package: "p", Name: "Set", Receiver: "C"},
	})
	got, partial, capped := ix.InterfaceCandidates(names, "p", "Both", 0, UncappedCandidates)
	want := []TypeKey{{Pkg: "p", Type: "C"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("embedded candidates = %+v, want %+v", got, want)
	}
	if partial || capped {
		t.Errorf("partial=%v capped=%v, want false/false", partial, capped)
	}
}

// TestInterfaceCandidatesCrossPackagePartial proves a cross-package embedded
// interface contributes only the names known and sets partial: the candidate set
// is a superset (types only need the known names).
func TestInterfaceCandidatesCrossPackagePartial(t *testing.T) {
	src := `package p

import ext "example.com/ext"

type Uses interface {
	ext.Extra
	Set(int)
}
`
	ix, names := candidateFixture(t, src, []Symbol{
		{ID: 1, Kind: parse.KindMethod, Package: "p", Name: "Set", Receiver: "A"},
	})
	required, partial, ok := names.InterfaceMethodNames("p", "Uses")
	if !ok || !partial {
		t.Fatalf("InterfaceMethodNames ok=%v partial=%v, want true/true", ok, partial)
	}
	if len(required) != 1 || !required["Set"] {
		t.Errorf("known names = %v, want {Set}", required)
	}
	got, partial, capped := ix.InterfaceCandidates(names, "p", "Uses", 0, UncappedCandidates)
	want := []TypeKey{{Pkg: "p", Type: "A"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("cross-package candidates = %+v, want %+v", got, want)
	}
	if !partial || capped {
		t.Errorf("partial=%v capped=%v, want true/false", partial, capped)
	}
}

// TestInterfaceCandidatesCycleTerminates proves an embedding cycle is bounded by
// the cycle guard and both interfaces' own methods are collected.
func TestInterfaceCandidatesCycleTerminates(t *testing.T) {
	src := `package p

type A interface {
	B
	AGet()
}

type B interface {
	A
	BGet()
}
`
	_, names := candidateFixture(t, src, nil)
	got, partial, ok := names.InterfaceMethodNames("p", "A")
	if !ok || partial {
		t.Fatalf("InterfaceMethodNames ok=%v partial=%v, want true/false", ok, partial)
	}
	if len(got) != 2 || !got["AGet"] || !got["BGet"] {
		t.Errorf("cycle names = %v, want {AGet,BGet}", got)
	}
}

// TestCapHistogramChooseN proves the fixed N rule (D-075): the smallest of
// {5,10,20,50,100} covering at least 95% of sites, and escalation when none
// qualifies.
func TestCapHistogramChooseN(t *testing.T) {
	h := NewCapHistogram()
	for _, c := range []int{1, 2, 3, 4, 10} {
		h.Add(c)
	}
	if got := h.Sites(); got != 5 {
		t.Errorf("sites = %d, want 5", got)
	}
	if got := h.CandidateTotal(); got != 20 {
		t.Errorf("candidate total = %d, want 20", got)
	}
	if got := h.Percentile(50); got != 3 {
		t.Errorf("p50 = %d, want 3", got)
	}
	if got := h.Max(); got != 10 {
		t.Errorf("max = %d, want 10", got)
	}
	if got := h.ChooseN(); got != 10 {
		t.Errorf("chosen N = %d, want 10", got)
	}

	big := NewCapHistogram()
	for i := 0; i < 20; i++ {
		big.Add(200)
	}
	if got := big.ChooseN(); got != 0 {
		t.Errorf("chosen N with no qualifying threshold = %d, want 0", got)
	}
	if r := big.Result(5); !r.Escalate || r.ChosenN != 0 {
		t.Errorf("result = %+v, want escalate with chosen N 0", r)
	}
}

// TestCapHistogramEscalatesAbove50 proves the N > 50 gate (D-075): a
// distribution where only 100 candidates clears the 95% threshold chooses
// N = 100 and escalates, so the cartographer stops instead of adopting it.
func TestCapHistogramEscalatesAbove50(t *testing.T) {
	h := NewCapHistogram()
	for i := 0; i < 19; i++ {
		h.Add(60)
	}
	h.Add(1) // 20 sites, 95% requires 19; only AtMost100 covers all 20.
	if got := h.ChooseN(); got != 100 {
		t.Fatalf("chosen N = %d, want 100", got)
	}
	r := h.Result(5)
	if r.ChosenN != 100 || !r.Escalate {
		t.Errorf("result = %+v, want chosen N 100 and escalate", r)
	}
}

// TestInterfaceCandidatesEmpty proves an interface with no known method names
// yields no candidates (an empty set never matches every type).
func TestInterfaceCandidatesEmpty(t *testing.T) {
	src := `package p

type Empty interface{}
`
	ix, names := candidateFixture(t, src, []Symbol{
		{ID: 1, Kind: parse.KindMethod, Package: "p", Name: "Get", Receiver: "A"},
	})
	got, partial, capped := ix.InterfaceCandidates(names, "p", "Empty", 0, UncappedCandidates)
	if len(got) != 0 || partial || capped {
		t.Errorf("empty-interface candidates = %+v partial=%v capped=%v, want none/false/false",
			got, partial, capped)
	}
}

// TestInterfaceCandidatesCapped proves the capped mode is deterministic: the
// candidate set is sorted before the cap is applied, an exact-fit cap does not
// report capped, and one over the cap does.
func TestInterfaceCandidatesCapped(t *testing.T) {
	src := `package p

type I interface {
	M()
}

type A struct{}

func (A) M() {}

type B struct{}

func (B) M() {}

type C struct{}

func (C) M() {}
`
	// Symbols are supplied out of order (C, A, B) to prove the result is sorted.
	ix, names := candidateFixture(t, src, []Symbol{
		{ID: 1, Kind: parse.KindMethod, Package: "p", Name: "M", Receiver: "C"},
		{ID: 2, Kind: parse.KindMethod, Package: "p", Name: "M", Receiver: "A"},
		{ID: 3, Kind: parse.KindMethod, Package: "p", Name: "M", Receiver: "B"},
	})
	want := []TypeKey{{Pkg: "p", Type: "A"}, {Pkg: "p", Type: "B"}, {Pkg: "p", Type: "C"}}

	got, partial, capped := ix.InterfaceCandidates(names, "p", "I", 3, CappedCandidates)
	if !reflect.DeepEqual(got, want) || partial || capped {
		t.Errorf("cap 3 (exact fit) = %+v partial=%v capped=%v, want %+v/false/false",
			got, partial, capped, want)
	}

	got, partial, capped = ix.InterfaceCandidates(names, "p", "I", 2, CappedCandidates)
	if !reflect.DeepEqual(got, want) || partial || !capped {
		t.Errorf("cap 2 (one over) = %+v partial=%v capped=%v, want %+v/false/true",
			got, partial, capped, want)
	}
}

// TestAnalyzeRule4Capped proves the capped Rule 4 vocabulary (spec story 98):
// with the cap set below the candidate count, the call stores one unresolved
// candidates_capped row (no callee) instead of ambiguous rows.
func TestAnalyzeRule4Capped(t *testing.T) {
	src := []byte(`package p

type I interface {
	M()
}

type A struct{}
type B struct{}
type C struct{}

func (A) M() {}
func (B) M() {}
func (C) M() {}

func F(i I) {
	i.M()
}
`)
	info, err := Extract(src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	syms := []Symbol{
		{ID: 1, Path: "p/p.go", Kind: parse.KindFunc, Package: "p", Name: "F", QualifiedName: "p.F", StartLine: 15},
		{ID: 2, Path: "p/p.go", Kind: parse.KindInterface, Package: "p", Name: "I", QualifiedName: "p.I"},
		{ID: 3, Path: "p/p.go", Kind: parse.KindMethod, Package: "p", Name: "M", Receiver: "A", QualifiedName: "p.(A).M"},
		{ID: 4, Path: "p/p.go", Kind: parse.KindMethod, Package: "p", Name: "M", Receiver: "B", QualifiedName: "p.(B).M"},
		{ID: 5, Path: "p/p.go", Kind: parse.KindMethod, Package: "p", Name: "M", Receiver: "C", QualifiedName: "p.(C).M"},
	}
	ix := NewIndex(syms)
	names := NewNameIndex()
	names.Add("p", map[string]string{"F": "func", "I": "type", "A": "type", "B": "type", "C": "type"})
	names.AddInterfaces("p", map[string]InterfaceFact{
		"I": {Methods: map[string]bool{"M": true}},
	})

	capped, st := Analyze("p/p.go", "p", info, ix, names, Rule4{Compute: true, N: 2})
	want := []Edge{{
		CallerID: 1, CalleeID: 0, CalleeRaw: "i.M", Resolution: ResolutionUnresolved,
		CallKind: KindInterfaceMethod, CallLine: 16, UnresolvedReason: ReasonCandidatesCap,
	}}
	if len(capped) != 1 || capped[0] != want[0] {
		t.Fatalf("capped edges = %+v, want %+v", capped, want)
	}
	if st.Unresolved != 1 || st.Reasons[ReasonCandidatesCap] != 1 ||
		st.ByKind[KindInterfaceMethod] != 1 || st.Ambiguous != 0 {
		t.Errorf("capped stats = %+v, want one candidates_capped interface_method row", st)
	}
	if err := st.Check(); err != nil {
		t.Errorf("capped invariant: %v", err)
	}

	uncapped, st := Analyze("p/p.go", "p", info, ix, names, Rule4{Compute: true, N: 3})
	if len(uncapped) != 3 || st.Ambiguous != 3 {
		t.Fatalf("N=3 edges = %+v stats ambiguous=%d, want 3 ambiguous rows", uncapped, st.Ambiguous)
	}
	if err := st.Check(); err != nil {
		t.Errorf("uncapped invariant: %v", err)
	}
}
