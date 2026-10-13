package graph

import "testing"

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
// call through the shadowed name is deferred rather than a wrong exact.
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
// receive variable count as locals, so a bare identifier call through one is
// deferred_local rather than a stored no_match.
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

// TestExtractQualifiedCallShape proves a selector call whose operand is a bare
// identifier carries the alias-qualified shape (qualifier and field), while a
// receiver-style selector and a chained call carry neither and so stay deferred.
func TestExtractQualifiedCallShape(t *testing.T) {
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
	type shape struct{ raw, name, qualifier, field string }
	var got []shape
	for _, c := range info.Decls[0].Calls {
		got = append(got, shape{c.Raw, c.Name, c.Qualifier, c.Field})
	}
	want := []shape{
		{"klog.V(2).InfoS", "", "", ""},
		{"klog.V", "", "klog", "V"},
		{"s.Method", "", "s", "Method"},
		{"s.field.Method", "", "", ""},
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
