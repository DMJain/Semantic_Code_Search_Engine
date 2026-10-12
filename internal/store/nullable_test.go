package store_test

import (
	"testing"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/store"
)

func TestNullIfEmpty(t *testing.T) {
	if got := store.NullIfEmpty(""); got != nil {
		t.Errorf("NullIfEmpty(\"\") = %v, want nil", got)
	}
	if got := store.NullIfEmpty("x"); got != "x" {
		t.Errorf("NullIfEmpty(\"x\") = %v, want x", got)
	}
}
