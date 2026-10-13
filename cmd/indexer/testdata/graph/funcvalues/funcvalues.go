package funcvalues

import depone "k8s.io/kubernetes/depone"

func PackageFunc() {}

var PackageVarFunc func()

var InferredFunc = func() {}

type Callback func()

var NamedFunc Callback

func localFuncVar() {
	var f func()
	f()
}

func paramFunc(f func()) {
	f()
}

func closureVar() {
	f := func() {}
	f()
}

func packageVar() {
	PackageVarFunc()
}

func inferredVar() {
	InferredFunc()
}

func namedVar() {
	NamedFunc()
}

func aliasVar() {
	depone.Hook()
}

func packageFunc() {
	PackageFunc()
}
