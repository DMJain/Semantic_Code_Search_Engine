package group

// NewFar builds a Far declared in another file, so it never groups.
func NewFar() *Far {
	return &Far{}
}
