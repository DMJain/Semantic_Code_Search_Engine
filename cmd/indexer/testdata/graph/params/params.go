package params

func multi(a, b int) {
	b()
}

func localGrouped() {
	var (
		x, y = 1, 2
	)
	y()
	_ = x
}

func localMulti() {
	var p, q int
	q()
	_ = p
}

func localConst() {
	const (
		m, n = 1, 2
	)
	n()
	_ = m
}
