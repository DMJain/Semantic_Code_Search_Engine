package scheduler

// Calls covers raw-call extraction (S1-T06): a method chain, a builtin
// (len/append/string/make), a conversion, a nested function literal and a
// repeated call. The golden pins the exact list, its source order and dedup.
// The tree-sitter parser does not type-check, so the identifiers need not
// resolve.
func Calls(a, b int, x any) {
	len(a)
	sched.cache.AssumePod()
	a.B().C()
	klog.V(2).InfoS("msg", "key", a)
	f := func() {
		helper(b)
	}
	f()
	append([]int{}, a)
	string(x)
	make([]int, b)
	MyType(a)
	any(x)
	Always()
	Always()
}

// Chain is a method with a deeper chain and a conversion. A conversion to a
// predeclared type (int64) is dropped; the chain's outer and inner calls are
// both kept, outer first.
func (s *Scheduler) Chain() {
	stringer.For(s).Handle()
	_ = int64(0)
}
