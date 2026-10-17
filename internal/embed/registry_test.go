package embed

import "testing"

// TestMaxDocPrefix pins the largest static document prefix across the registry:
// nomic's "search_document: " (17 bytes, 5 estimated tokens). The chunker
// derives its header budget from it (D-082).
func TestMaxDocPrefix(t *testing.T) {
	if got, want := MaxDocPrefix(), "search_document: "; got != want {
		t.Errorf("MaxDocPrefix() = %q, want %q", got, want)
	}
	if n := len(MaxDocPrefix()); n != 17 {
		t.Errorf("len(MaxDocPrefix()) = %d, want 17", n)
	}
}
