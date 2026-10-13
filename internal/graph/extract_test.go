package graph

import (
	"strings"
	"testing"
)

// TestExtractImportAliases proves the per-file import alias map (spec story 38):
// an explicit alias wins, an unaliased import uses the last path segment, and
// dot and blank imports are ignored.
func TestExtractImportAliases(t *testing.T) {
	src := []byte(`package p

import (
	d "a/b"
	. "a/c"
	_ "a/d"
	"a/e"
)

func F() {}
`)
	info, err := Extract(src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	want := map[string]string{"d": "a/b", "e": "a/e"}
	if len(info.Aliases) != len(want) {
		t.Fatalf("aliases = %v, want %v", info.Aliases, want)
	}
	for alias, path := range want {
		if info.Aliases[alias] != path {
			t.Errorf("alias %q = %q, want %q", alias, info.Aliases[alias], path)
		}
	}
	if _, ok := info.Aliases["."]; ok {
		t.Error("dot import leaked an alias")
	}
	if _, ok := info.Aliases["_"]; ok {
		t.Error("blank import leaked an alias")
	}
}

// TestDefaultAlias proves the default alias for an unaliased import: the last
// path segment, except a major-version module suffix `/vN` (N >= 2) whose
// package name is the parent segment. `/v1` and `v1beta1` stay as written.
func TestDefaultAlias(t *testing.T) {
	cases := []struct{ path, want string }{
		{"k8s.io/klog/v2", "klog"},
		{"k8s.io/api/core/v1", "v1"},
		{"github.com/onsi/ginkgo/v2", "ginkgo"},
		{"example.com/foo/v10", "foo"},
		{"k8s.io/client-go/listers/core/v1beta1", "v1beta1"},
		{"fmt", "fmt"},
		{"v2", "v2"}, // no slash: a bare version segment is not a module suffix
	}
	for _, c := range cases {
		if got := defaultAlias(c.path); got != c.want {
			t.Errorf("defaultAlias(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}

// TestExtractUnaliasedVersionedImport proves an unaliased versioned import binds
// its package name (the parent segment), so a call through it carries the right
// qualifier instead of the `v2` segment.
func TestExtractUnaliasedVersionedImport(t *testing.T) {
	src := []byte(`package p

import "k8s.io/klog/v2"

func F() {
	klog.V(2).InfoS("x")
}
`)
	info, err := Extract(src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if got := info.Aliases["klog"]; got != "k8s.io/klog/v2" {
		t.Errorf("alias klog = %q, want the versioned import path (aliases = %v)", got, info.Aliases)
	}
	if _, ok := info.Aliases["v2"]; ok {
		t.Error("the v2 segment leaked as an alias")
	}
}

// TestExtractTypeEnvironment proves the per-function type environment records
// the receiver and parameter types as written, keeps an unknown written form
// (func(), error, slices, maps) so the resolver can reject it, and removes a
// name redeclared in the body (an inner := shadows the parameter, so it is
// unknown and never a wrong exact).
func TestExtractTypeEnvironment(t *testing.T) {
	src := []byte(`package p

func (s *S) M(a *Foo, i Interface, f func(), xs []int, err error, b byte, p, q Foo) {
	x := 1
	_ = x
}

type S struct{}
type Foo struct{}
type Interface interface{ N() }
`)
	info, err := Extract(src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(info.Decls) != 1 {
		t.Fatalf("decls = %d, want 1", len(info.Decls))
	}
	types := info.Decls[0].Types
	want := map[string]string{
		"s": "*S", "a": "*Foo", "i": "Interface", "f": "func()", "xs": "[]int", "err": "error",
		"p": "Foo", "q": "Foo",
	}
	for name, typ := range want {
		if types[name] != typ {
			t.Errorf("type of %q = %q, want %q (types = %v)", name, types[name], typ, types)
		}
	}
	if _, ok := types["x"]; ok {
		t.Errorf("shadowed name x still has a type: %v", types)
	}
}

// TestExtractTypeParameters proves the declaration's own type parameters are
// collected, so the resolver can treat a receiver or parameter typed as a type
// parameter as unknown and never guess a same-named package type.
func TestExtractTypeParameters(t *testing.T) {
	src := []byte(`package p

func F[T any](x T) {
	x.M()
}

func G() {}
`)
	info, err := Extract(src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if !info.Decls[0].TypeParams["T"] {
		t.Errorf("type parameter T not recorded: %v", info.Decls[0].TypeParams)
	}
	if len(info.Decls[1].TypeParams) != 0 {
		t.Errorf("G type parameters = %v, want none", info.Decls[1].TypeParams)
	}
}

// TestExtractTypeEnvironmentShadowedParam proves an inner declaration of a name
// removes the parameter's type (the flat per-declaration local scope), so a
// call through the shadowed name is a func_value rather than a wrong exact.
func TestExtractTypeEnvironmentShadowedParam(t *testing.T) {
	src := []byte(`package p

func F(a *Foo) {
	{
		a := 1
		_ = a
	}
}

type Foo struct{}
`)
	info, err := Extract(src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if _, ok := info.Decls[0].Types["a"]; ok {
		t.Errorf("shadowed parameter a still has a type: %v", info.Decls[0].Types)
	}
}

// TestExtractTypeSwitchAndReceiveLocals proves a type-switch guard and a select
// receive variable count as locals, so a bare identifier call through one is a
// func_value rather than a stored no_match.
func TestExtractTypeSwitchAndReceiveLocals(t *testing.T) {
	src := []byte(`package p

func F(y interface{}, ch chan int) {
	switch s := y.(type) {
	case int:
		s()
	}
	select {
	case r := <-ch:
		r()
	}
}
`)
	info, err := Extract(src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	locals := info.Decls[0].Locals
	for _, name := range []string{"s", "r"} {
		if !locals[name] {
			t.Errorf("%q is not a local (locals = %v)", name, locals)
		}
	}
}

// TestExtractCallShape proves a call's selector shape: an identifier is a bare
// call, an alias-qualified selector carries its qualifier, a chained call
// carries its field with no qualifier, and a field chain carries the base
// qualifier, the intermediate field names and the called field.
func TestExtractCallShape(t *testing.T) {
	src := []byte(`package p

import klog "k8s.io/klog/v2"

func F() {
	klog.V(2).InfoS("x")
	s.Method()
	s.field.Method()
}
`)
	info, err := Extract(src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(info.Decls) != 1 {
		t.Fatalf("decls = %d, want 1", len(info.Decls))
	}
	type shape struct {
		raw, name, qualifier, field string
		fields                      string
	}
	var got []shape
	for _, c := range info.Decls[0].Calls {
		got = append(got, shape{c.Raw, c.Name, c.Qualifier, c.Field, strings.Join(c.Fields, ",")})
	}
	want := []shape{
		{"klog.V(2).InfoS", "", "", "InfoS", ""},
		{"klog.V", "", "klog", "V", ""},
		{"s.Method", "", "s", "Method", ""},
		{"s.field.Method", "", "s", "Method", "field"},
	}
	if len(got) != len(want) {
		t.Fatalf("calls = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("call %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestExtractLocalTypes proves the inferred local type environment (spec story
// 39): `var x T`, `var x = T{}`, `x := T{}` and `x := &T{}` give the written
// type T, a constructor call records the constructor name for the resolver, and
// the field chain of `s.cache.Assume` carries the base qualifier, the
// intermediate field and the called field.
func TestExtractLocalTypes(t *testing.T) {
	src := []byte(`package p

type T struct{}
type Cache struct{}
type Scheduler struct{ cache *Cache }

func (T) M() {}
func (*Cache) Assume() {}
func NewT() T { return T{} }
func NewT2() (T, error) { return T{}, nil }

func F(s *Scheduler) {
	var x T
	x.M()
	y := T{}
	y.M()
	z := &T{}
	z.M()
	w := NewT()
	w.M()
	v := NewT2()
	v.M()
	s.cache.Assume()
}
`)
	info, err := Extract(src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	d := findDecl(t, info, "F")
	for name, want := range map[string]string{"s": "*Scheduler", "x": "T", "y": "T", "z": "T"} {
		if got := d.Types[name]; got != want {
			t.Errorf("type of %q = %q, want %q (types = %v)", name, got, want, d.Types)
		}
	}
	if _, ok := d.Types["w"]; ok {
		t.Errorf("constructor local w should not have a written type yet: %v", d.Types)
	}
	if got := d.ConstructorLocals["w"]; got != "NewT" {
		t.Errorf("constructor local w = %q, want NewT", got)
	}
	if got := d.ConstructorLocals["v"]; got != "NewT2" {
		t.Errorf("constructor local v = %q, want NewT2 (two results stay unknown at Analyze)", got)
	}
	var chain *Call
	for i := range d.Calls {
		if d.Calls[i].Raw == "s.cache.Assume" {
			chain = &d.Calls[i]
		}
	}
	if chain == nil {
		t.Fatalf("field chain call not extracted: %+v", d.Calls)
	}
	if chain.Qualifier != "s" || chain.Field != "Assume" || strings.Join(chain.Fields, ",") != "cache" {
		t.Errorf("field chain = %+v, want qualifier s, field Assume, fields [cache]", *chain)
	}
}

// TestExtractCapsLongCallRaw proves a call whose function is a long expression
// (here an immediately-invoked function literal) has its raw callee text capped,
// so the stored callee_raw can never overflow the edges_uniq btree index.
func TestExtractCapsLongCallRaw(t *testing.T) {
	body := strings.Repeat("\t\t_ = 1\n", 400)
	src := []byte("package p\n\nfunc F() {\n\tfunc() {\n" + body + "\t}()\n}\n")
	info, err := Extract(src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	d := findDecl(t, info, "F")
	if len(d.Calls) == 0 {
		t.Fatal("the immediately-invoked function literal was not extracted")
	}
	for _, c := range d.Calls {
		if len(c.Raw) > maxCallRaw {
			t.Errorf("raw call length = %d, want at most %d", len(c.Raw), maxCallRaw)
		}
	}
}

// TestExtractShadowedLocalStaysUnknown proves a local redeclared in the body
// (here a var then a nested :=) is unknown, so shadowing wins and the resolver
// never inherits an outer type.
func TestExtractShadowedLocalStaysUnknown(t *testing.T) {
	src := []byte(`package p

type T struct{}

func F() {
	var x T
	{
		x := 0
		_ = x
	}
}
`)
	info, err := Extract(src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	d := findDecl(t, info, "F")
	if _, ok := d.Types["x"]; ok {
		t.Errorf("redeclared local x still has a type: %v", d.Types)
	}
}

// TestSourceFacts proves the names-only facts: grouped struct fields both get
// their type, an embedded field is skipped (promotion is unseen), a bare type is
// a same-package TypeRef, and a single-result function is a constructor while
// zero-, two- and grouped-named-result functions are not.
func TestSourceFacts(t *testing.T) {
	src := []byte(`package p

type Scheduler struct {
	cache *Cache
	Inner
	a, b int
}
type Cache struct{}
type Inner struct{}

func NewT() T { return T{} }
func NewT2() (T, error) { return T{}, nil }
func NamedTwo() (x, y T) { return T{}, T{} }
func NoResult() {}
`)
	facts, err := SourceFacts(src)
	if err != nil {
		t.Fatalf("SourceFacts: %v", err)
	}
	for key, want := range map[string]TypeRef{
		"Scheduler\x00cache": {Name: "Cache"},
		"Scheduler\x00a":     {Name: "int"},
		"Scheduler\x00b":     {Name: "int"},
	} {
		if got := facts.Fields[key]; got != want {
			t.Errorf("field %q = %+v, want %+v (fields = %v)", key, got, want, facts.Fields)
		}
	}
	if _, ok := facts.Fields["Scheduler\x00Inner"]; ok {
		t.Errorf("embedded field should be skipped: %v", facts.Fields)
	}
	if got := facts.Results["NewT"]; got != (TypeRef{Name: "T"}) {
		t.Errorf("constructor NewT = %+v, want {T}", got)
	}
	for _, name := range []string{"NewT2", "NamedTwo", "NoResult"} {
		if ref, ok := facts.Results[name]; ok {
			t.Errorf("%s should not be a single-result constructor, got %+v", name, ref)
		}
	}
}

// TestSourceFactsFuncVariables proves the declared-name index marks a
// package-level function variable as kind "func_value" for all four shapes: a
// written function type, an inferred function-literal initialiser, a written
// type that names a package-level function type, and a written type that names a
// package-level function type alias. A package-level declared function stays
// "func" and a plain var stays "var".
func TestSourceFactsFuncVariables(t *testing.T) {
	src := []byte(`package p

type Callback func(int)

type Alias = func(int)

var Written func()

var Inferred = func() {}

var Named Callback

var Aliased Alias

var Plain int

const C = 1

func Declared() {}
`)
	facts, err := SourceFacts(src)
	if err != nil {
		t.Fatalf("SourceFacts: %v", err)
	}
	for name, want := range map[string]string{
		"Written":  "func_value",
		"Inferred": "func_value",
		"Named":    "func_value",
		"Aliased":  "func_value",
		"Plain":    "var",
		"C":        "const",
		"Declared": "func",
		"Callback": "type",
		"Alias":    "type",
	} {
		if got := facts.Declared[name]; got != want {
			t.Errorf("Declared[%q] = %q, want %q", name, got, want)
		}
	}
}

// TestSourceFactsResolvesDeclaringAlias proves a qualified field and constructor
// result type is resolved against the DECLARING file's import aliases at
// extraction time, storing the import path rather than the raw alias text, so a
// caller file binding the same alias to another package cannot redirect it.
func TestSourceFactsResolvesDeclaringAlias(t *testing.T) {
	src := []byte(`package p

import foo "example.com/x"

type Scheduler struct {
	queue foo.Queue
}

func NewQueue() foo.Queue { return foo.Queue{} }
`)
	facts, err := SourceFacts(src)
	if err != nil {
		t.Fatalf("SourceFacts: %v", err)
	}
	want := TypeRef{Pkg: "example.com/x", Name: "Queue"}
	if got := facts.Fields["Scheduler\x00queue"]; got != want {
		t.Errorf("field queue = %+v, want %+v", got, want)
	}
	if got := facts.Results["NewQueue"]; got != want {
		t.Errorf("constructor NewQueue = %+v, want %+v", got, want)
	}
}

// findDecl returns the declaration with the given name or fails.
func findDecl(t *testing.T, info FileInfo, name string) Decl {
	t.Helper()
	for _, d := range info.Decls {
		if d.Name == name {
			return d
		}
	}
	t.Fatalf("declaration %q not found in %+v", name, info.Decls)
	return Decl{}
}
