package alias

import (
	"k8s.io/klog/v2"
	_ "k8s.io/kubernetes/blankpkg"
	depone "k8s.io/kubernetes/depone"
	deptwo "k8s.io/kubernetes/deptwo"
	. "k8s.io/kubernetes/dotpkg"
	"k8s.io/kubernetes/typed"

	cache "k8s.io/client-go/tools/cache"
)

func callOne() {
	depone.Name()
}

func callTwo() {
	deptwo.Name()
}

func callDot() {
	DotOnly()
}

func callBlank() {
	blankpkg.BlankOnly()
}

func callConvert(x int) {
	_ = typed.Type(x)
}

func callExternal() {
	klog.V(2).InfoS("x")
}

func callMissing() {
	depone.Missing()
}

func callStaging() {
	cache.SomeFunc()
}

func callShadowed(klog *fakeKlog) {
	klog.V(2).InfoS("x")
}

type fakeKlog struct{}

func (fakeKlog) V(int) fakeKlog { return fakeKlog{} }
func (fakeKlog) InfoS(string)   {}
