package scheduler

// runner is a fixture interface with one implementing method, so the VTA
// ground-truth graph exercises an interface dispatch and a concrete method.
type runner interface {
	run()
}

type impl struct{}

func (impl) run() {}

// Schedule schedules a fixture pod.
func Schedule() {
	helper()
	var r runner = impl{}
	r.run()
}

func helper() {}
