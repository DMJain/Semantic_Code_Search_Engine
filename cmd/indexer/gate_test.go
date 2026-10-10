package main

import "testing"

func TestDrawGateDeterministic(t *testing.T) {
	rows := make([]gateEntry, 10)
	for i := range rows {
		rows[i] = gateEntry{ID: int64(i), QualifiedName: string(rune('a' + i))}
	}

	first := drawGate(rows, 3, 3)
	if len(first) != 3 {
		t.Fatalf("draw 3 of 10: got %d", len(first))
	}
	again := drawGate(rows, 3, 3)
	for i := range first {
		if first[i].ID != again[i].ID {
			t.Fatalf("same seed differs at %d: %d vs %d", i, first[i].ID, again[i].ID)
		}
	}
	other := drawGate(rows, 3, 4)
	same := true
	for i := range first {
		if first[i].ID != other[i].ID {
			same = false
			break
		}
	}
	if same {
		t.Fatal("different seed gave the same draw")
	}

	if got := drawGate(rows, 99, 3); len(got) != len(rows) {
		t.Fatalf("draw above size should clamp to %d, got %d", len(rows), len(got))
	}
	if got := drawGate(rows, -1, 3); len(got) != 0 {
		t.Fatalf("negative draw should clamp to 0, got %d", len(got))
	}
}
