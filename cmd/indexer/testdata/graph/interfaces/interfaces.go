package interfaces

import "k8s.io/kubernetes/ifaceext"

// Store has two methods; A is the only type that declares both (A.Get is a
// value receiver, A.Set a pointer receiver). B declares only Get.
type Store interface {
	Get() int
	Set(int)
}

type A struct{}

func (A) Get() int { return 0 }
func (*A) Set(int) {}

type B struct{}

func (B) Get() int { return 0 }

// Both embeds Base (same package), so its method set is Read and Write.
type Base interface {
	Read() int
}

type Both interface {
	Base
	Write(int)
}

type C struct{}

func (C) Read() int { return 0 }
func (C) Write(int) {}

type D struct{}

func (D) Read() int { return 0 }
func (D) Write(int) {}

// UsesExt embeds a cross-package interface, so its known method names are only
// Write and its candidate set is a superset (interfaces_partial).
type UsesExt interface {
	ifaceext.Extra
	Write(int)
}

// Empty has no known method names, so it produces no Rule 4 candidates (and
// falls through to Rule 5).
type Empty interface{}

// Cap3 has exactly three implementers; a cap of 3 stores one ambiguous row per
// implementer. Cap4 has four, so the same cap stores one candidates_capped row.
type Cap3 interface {
	Do3()
}

type Cap4 interface {
	Do4()
}

type Q1 struct{}

func (Q1) Do3() {}

type Q2 struct{}

func (Q2) Do3() {}

type Q3 struct{}

func (Q3) Do3() {}

type R1 struct{}

func (R1) Do4() {}

type R2 struct{}

func (R2) Do4() {}

type R3 struct{}

func (R3) Do4() {}

type R4 struct{}

func (R4) Do4() {}

// NoImpl has a known method no indexed type declares, so it stores no_match.
type NoImpl interface {
	Missing()
}

func callStore(s Store) {
	s.Get()
	s.Set(0)
}

func callBoth(b Both) {
	b.Write(0)
}

func callUsesExt(u UsesExt) {
	u.Write(0)
}

func callCap3(c Cap3) {
	c.Do3()
}

func callCap4(c Cap4) {
	c.Do4()
}

func callNoImpl(n NoImpl) {
	n.Missing()
}

// callEmpty is a Rule 4 fall-through: Empty has no known method, so the call is
// looked up by Rule 5 (one ambiguous row per same-package Get method).
func callEmpty(e Empty) {
	e.Get()
}
