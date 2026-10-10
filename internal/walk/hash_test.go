package walk_test

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/walk"
)

// TestSliceHashKnownValue pins the frozen path-list format: paths one per line,
// LF-terminated, bytewise-sorted, sha256 over that byte stream. The expected
// value is the shell result of
// `printf '%s\n' pkg/scheduler/a.go pkg/kubelet/b.go pkg/controller/c.go | LC_ALL=C sort | shasum -a 256`.
func TestSliceHashKnownValue(t *testing.T) {
	paths := []string{"pkg/scheduler/a.go", "pkg/kubelet/b.go", "pkg/controller/c.go"}
	const want = "202c416597188a7673d7579400108fc68c3ed85a28ec9bd40af60f1c95bd4bba"
	if got := walk.SliceHash(paths); got != want {
		t.Fatalf("SliceHash = %s, want %s", got, want)
	}
}

// TestSliceHashOrderIndependent proves the hash sorts before digesting, so the
// query order is irrelevant.
func TestSliceHashOrderIndependent(t *testing.T) {
	a := walk.SliceHash([]string{"pkg/scheduler/a.go", "pkg/kubelet/b.go", "pkg/controller/c.go"})
	b := walk.SliceHash([]string{"pkg/kubelet/b.go", "pkg/controller/c.go", "pkg/scheduler/a.go"})
	if a != b {
		t.Fatalf("SliceHash depends on input order: %s != %s", a, b)
	}
}

// TestSliceHashEmpty pins the empty-input digest (sha256 of the empty string).
func TestSliceHashEmpty(t *testing.T) {
	const want = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if got := walk.SliceHash(nil); got != want {
		t.Fatalf("SliceHash(nil) = %s, want %s", got, want)
	}
}

// TestExpectedSliceHashIsHex keeps the embedded expected hash well-formed.
func TestExpectedSliceHashIsHex(t *testing.T) {
	h := walk.ExpectedSliceHash()
	if len(h) != 64 {
		t.Fatalf("ExpectedSliceHash length = %d, want 64", len(h))
	}
	if _, err := hex.DecodeString(h); err != nil {
		t.Fatalf("ExpectedSliceHash is not hex: %v", err)
	}
}

// TestVerifySliceHashDrift is the drift failure mode a drop-list edit produces:
// a path set other than the frozen one must fail and the error must name both
// the expected and the computed hash. It never touches the real slice.sha256.
func TestVerifySliceHashDrift(t *testing.T) {
	paths := []string{"pkg/scheduler/a.go"}
	err := walk.VerifySliceHash(paths)
	if err == nil {
		t.Fatal("VerifySliceHash accepted a drifted path list")
	}
	if !strings.Contains(err.Error(), walk.ExpectedSliceHash()) {
		t.Errorf("error %q does not name the expected hash", err)
	}
	if got := walk.SliceHash(paths); !strings.Contains(err.Error(), got) {
		t.Errorf("error %q does not name the computed hash %s", err, got)
	}
}
