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

// Empty has no known method names, so it produces no Rule 4 candidates.
type Empty interface{}

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
