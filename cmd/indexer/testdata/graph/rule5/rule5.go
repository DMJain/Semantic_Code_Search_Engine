package rule5

type A struct{}

func (A) Only() {}

type B struct{}

func (B) Two() {}

type C struct{}

func (C) Two() {}

func one(y interface{}) {
	y.Only()
}

func two(y interface{}) {
	y.Two()
}

func none(y interface{}) {
	y.Missing()
}
