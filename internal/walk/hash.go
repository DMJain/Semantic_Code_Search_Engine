package walk

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// sliceHashFile is the frozen sha256 of the sorted in-slice path list, recorded
// when the slice was frozen (D-070) and embedded so the binary carries the expected value.
//
//go:embed slice.sha256
var sliceHashFile string

// ExpectedSliceHash returns the frozen sha256 of the sorted in-slice path list
// (D-070).
func ExpectedSliceHash() string {
	return strings.TrimSpace(sliceHashFile)
}

// SliceHash returns the sha256 of paths in the frozen path-list format:
// repo-root-relative paths with "/" separators, one per line, LF-terminated,
// bytewise-sorted. It byte-matches `... | LC_ALL=C sort | shasum -a 256` over
// the same paths (D-070).
func SliceHash(paths []string) string {
	sorted := append([]string(nil), paths...)
	sort.Strings(sorted)
	h := sha256.New()
	for _, p := range sorted {
		h.Write([]byte(p))
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// VerifySliceHash compares SliceHash(paths) with ExpectedSliceHash and returns
// an error naming both hashes on drift.
func VerifySliceHash(paths []string) error {
	got := SliceHash(paths)
	want := ExpectedSliceHash()
	if got != want {
		return fmt.Errorf("slice hash drift: expected %s, computed %s", want, got)
	}
	return nil
}
