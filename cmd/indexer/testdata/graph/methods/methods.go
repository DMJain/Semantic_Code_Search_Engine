package methods

type Scheduler struct{}

func (s *Scheduler) Run() {
	s.Tick()
}

func (s *Scheduler) Tick() {}

type Widget struct{}

func (Widget) Tick() {}

type Box struct{}

func (Box) M() {}

type Crate struct{}

func (Crate) M() {}

type Interface interface {
	IMethod()
}

type Outer struct {
	Inner
}

type Inner struct{}

func (Inner) M() {}

func pointerReceiver(s *Scheduler) {
	s.Run()
}

func valueReceiver(w Widget) {
	w.Tick()
}

func parameterTyped(x *Box) {
	x.M()
}

func sameNameOnTwoTypes(a Box, c Crate) {
	a.M()
	c.M()
}

func interfaceParam(i Interface) {
	i.IMethod()
}

func embeddedUnknown(o Outer) {
	o.M()
}

func shadowedParameter(x *Box) {
	x := 0
	x.M()
}

func typeSwitchShadow(s *Scheduler, y interface{}) {
	switch s := y.(type) {
	case *Widget:
		s.Tick()
	}
}

func receiveShadow(s *Scheduler, ch chan int) {
	select {
	case s := <-ch:
		s.Run()
	}
}

type T struct{}

func (T) M() {}

func typeParam[T any](x T) {
	x.M()
}
