package scheduler

// runner is a fixture interface with one implementing method, so the VTA
// ground-truth graph exercises an interface dispatch and a concrete method.
type runner interface {
	run()
}

type impl struct{}

func (impl) run() {}

// Schedule exercises all four ground-truth call kinds: a static function, a
// concrete method, an interface dispatch and a function value. It also calls a
// generic (instantiated) and a function that contains a closure.
func Schedule() int {
	helper()
	var r runner = impl{}
	r.run()
	c := impl{}
	c.run()
	f := helper
	f()
	Closure()
	Apply(0, helper)
	h := &Heap[int]{}
	h.AddOrUpdate(1)
	ctr := counter{}
	(&ctr).Inc()
	return 0
}

// helper returns a value so it can also initialise a package variable.
func helper() int { return 0 }

// Heap is a generic type whose pointer-receiver method's receiver spelling
// (with its type parameter) must match internal/parse, so the edge maps rather
// than being counted gt_unmapped.
type Heap[T any] struct{ items []T }

// AddOrUpdate is a generic pointer-receiver method (story 15).
func (h *Heap[T]) AddOrUpdate(v T) int { return helper() }

// counter has a plain pointer-receiver method (story 15).
type counter struct{ n int }

// Inc is a pointer-receiver method on a non-generic type.
func (c *counter) Inc() int { return helper() }

// Apply is generic: its instantiation must map back to this origin symbol.
func Apply[T any](v T, fn func() int) {
	fn()
}

// Closure contains a function literal that calls helper; the ground-truth edge
// must be attributed to Closure, not to the anonymous function.
func Closure() {
	call := func() int { return helper() }
	call()
}

// packageVar is a package-level initialiser: its SSA caller is synthetic and is
// dropped and counted rather than mapped to a symbol.
var packageVar = helper()
