package scheduler

// Lone is a documented lone struct.
type Lone struct {
	FieldA int
	FieldB string
}

type Plain interface {
	Do(a, b int) error
}

// Set is a generic struct with a comparable type parameter.
type Set[T comparable] struct {
	members map[T]struct{}
}

// ReadWriter embeds two interfaces.
type ReadWriter interface {
	Reader
	Writer
}

type Alias = Lone

type MyInt int

type (
	// GroupedStruct is declared in a grouped block.
	GroupedStruct struct {
		X int
	}

	// GroupedInterface is also declared in the group.
	GroupedInterface interface {
		M() error
	}
)
