package summarize

import (
	"fmt"
	"slices"
	"testing"
)

// pinCandidates is a synthetic candidate set over the three in-slice roots:
// 36 + 24 + 12 functions with ids that do not follow name order.
func pinCandidates() []Candidate {
	var cands []Candidate
	id := int64(1000)
	for _, rc := range []struct {
		root string
		n    int
	}{{"pkg/scheduler", 12}, {"pkg/kubelet", 36}, {"pkg/controller", 24}} {
		for i := 0; i < rc.n; i++ {
			id += 7
			cands = append(cands, Candidate{ID: id, QualifiedName: fmt.Sprintf("k8s.io/kubernetes/%s.Fn%02d", rc.root, i), Root: rc.root})
		}
	}
	return cands
}

// pinFiles is a synthetic file set spread over every size stratum.
func pinFiles() []FileCandidate {
	var files []FileCandidate
	for i := 0; i < 40; i++ {
		files = append(files, FileCandidate{Path: fmt.Sprintf("pkg/kubelet/f%02d.go", i), Eligible: 1 + (i*i)%30})
	}
	return files
}

// TestSampleSeed3Pinned pins which symbols seed 3 draws, recorded from the
// implementation before the shared-helper refactor, so the refactor cannot
// change a draw.
func TestSampleSeed3Pinned(t *testing.T) {
	got := Sample(pinCandidates(), 20, 3)
	want := []int64{1035, 1007, 1028, 1133, 1217, 1126, 1203, 1091, 1119, 1252, 1175, 1189, 1266, 1448, 1434, 1455, 1350, 1413, 1420, 1364}
	if !slices.Equal(got, want) {
		t.Errorf("Sample seed 3 = %v, want %v", got, want)
	}
}

// TestSampleFilesSeed3Pinned pins which files seed 3 draws (see above).
func TestSampleFilesSeed3Pinned(t *testing.T) {
	var got []string
	for _, f := range SampleFiles(pinFiles(), 12, 3) {
		got = append(got, f.Path)
	}
	want := []string{"pkg/kubelet/f01.go", "pkg/kubelet/f00.go", "pkg/kubelet/f32.go", "pkg/kubelet/f02.go", "pkg/kubelet/f03.go", "pkg/kubelet/f36.go", "pkg/kubelet/f09.go", "pkg/kubelet/f34.go", "pkg/kubelet/f07.go", "pkg/kubelet/f13.go", "pkg/kubelet/f25.go", "pkg/kubelet/f35.go"}
	if !slices.Equal(got, want) {
		t.Errorf("SampleFiles seed 3 = %q, want %q", got, want)
	}
}
