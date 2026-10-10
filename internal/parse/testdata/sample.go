package scheduler

import "context"

// Documented has a two-part doc comment.
//
// The second paragraph is behind an empty comment line.
func Documented() int {
	return 0
}

// ValueReceiver is a method with a value receiver.
func (s Scheduler) ValueReceiver() {}

// PointerReceiver is a method with a pointer receiver.
func (s *Scheduler) PointerReceiver(ctx context.Context) (ScheduleResult, error) {
	return ScheduleResult{}, nil
}

// Generic declares a type parameter.
func Generic[T any](in T) T {
	return in
}

// MultiReturn returns several values.
func MultiReturn() (int, error) {
	return 0, nil
}

// Variadic takes a variadic parameter.
func Variadic(prefix string, rest ...int) {}

// Unnamed is separated from its doc by a blank line.

func Unnamed() {}

// Embedded is a method whose receiver type is an embedded field type.
func (e embedded) Embedded() {}
