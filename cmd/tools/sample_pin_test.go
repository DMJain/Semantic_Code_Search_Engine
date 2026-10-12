package main

import (
	"fmt"
	"slices"
	"testing"
)

// wantSeed3 is the seed-3 draw of 10 from fn000..fn099, recorded from the
// audit and gate samplers before they shared one helper.
var wantSeed3 = []string{
	"k8s.io/kubernetes/pkg/p.fn026", "k8s.io/kubernetes/pkg/p.fn043", "k8s.io/kubernetes/pkg/p.fn022",
	"k8s.io/kubernetes/pkg/p.fn007", "k8s.io/kubernetes/pkg/p.fn035", "k8s.io/kubernetes/pkg/p.fn050",
	"k8s.io/kubernetes/pkg/p.fn058", "k8s.io/kubernetes/pkg/p.fn001", "k8s.io/kubernetes/pkg/p.fn094",
	"k8s.io/kubernetes/pkg/p.fn067",
}

func TestGateDrawSeed3Pinned(t *testing.T) {
	var rows []gateEntry
	for i := 0; i < 100; i++ {
		rows = append(rows, gateEntry{QualifiedName: fmt.Sprintf("k8s.io/kubernetes/pkg/p.fn%03d", i)})
	}
	var got []string
	for _, r := range drawGate(rows, 10, 3) {
		got = append(got, r.QualifiedName)
	}
	if !slices.Equal(got, wantSeed3) {
		t.Errorf("gate seed 3 = %q, want %q", got, wantSeed3)
	}
}
