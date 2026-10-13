package plain

type Foo int

func Target() {}

func caller(a Foo, s string, b byte) {
	Target()
	len(s)
	string(b)
	Foo(1)
	x := 1
	x()
	a.Method()
	Missing()
	run := func() { Target() }
	_ = run
}
