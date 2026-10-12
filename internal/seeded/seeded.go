// Package seeded is the one place seeded sampling is defined, so "seed 3"
// draws the same items in the audit, the quality gate and the symbol and file
// samplers. Draws use math/rand, whose sequence for a seed is stable.
package seeded

import (
	"math/rand"
	"slices"
)

// Draw returns n items drawn reproducibly from items: items must already be in
// a deterministic order, the shuffle is seeded by seed and the first n are kept.
// n is clamped to [0, len(items)]; items is not modified.
func Draw[T any](items []T, n int, seed int64) []T {
	return Stratified([][]T{items}, []int{n}, seed)
}

// Stratified shuffles each group with one generator seeded by seed, in group
// order, and returns the first quotas[i] items of group i (clamped to the
// group's size, and to zero) concatenated in group order. Groups are not
// modified.
func Stratified[T any](groups [][]T, quotas []int, seed int64) []T {
	rng := rand.New(rand.NewSource(seed))
	var out []T
	for i, g := range groups {
		shuffled := slices.Clone(g)
		rng.Shuffle(len(shuffled), func(a, b int) { shuffled[a], shuffled[b] = shuffled[b], shuffled[a] })
		q := max(min(quotas[i], len(shuffled)), 0)
		out = append(out, shuffled[:q]...)
	}
	return out
}
