package locals

type T struct{}

func (T) M() {}

type Cache struct{}

func (c *Cache) Assume() {}

type Scheduler struct {
	cache *Cache
}

type Inner struct{}

func (Inner) Promoted() {}

type Outer struct {
	Inner
}

type Builder struct{}

func (b *Builder) B() *Builder { return b }
func (b *Builder) C()          {}

func NewT() T { return T{} }

func NewT2() (T, error) { return T{}, nil }

func varDecl() {
	var x T
	x.M()
}

func varLiteral() {
	var x = T{}
	x.M()
}

func composite() {
	x := T{}
	x.M()
}

func address() {
	x := &T{}
	x.M()
}

func constructor() {
	x := NewT()
	x.M()
}

func constructorTwo() {
	x := NewT2()
	x.M()
}

func fieldChain(s *Scheduler) {
	s.cache.Assume()
}

func embedded(o *Outer) {
	o.Promoted()
}

func shadowed() {
	var x T
	{
		x := 0
		x.M()
	}
}

func chain(a *Builder) {
	a.B().C()
}
