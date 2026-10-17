package basic

// Documented is a plain documented function.
func Documented() {}

func Plain() {}

type Widget struct{}

func (w Widget) Method() {}

/* BlockCommented has a block comment above it. */
func BlockCommented() {}

// +k8s:deepcopy-gen=true
// MarkerDoc follows a marker line.
func MarkerDoc() {}

// Detached is separated from its function by a blank line.

func Detached() {}

//go:noinline
func NoInline() {}

func UsesPod() {
	schedulePod()
}
