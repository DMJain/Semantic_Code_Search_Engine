package summarize

import "testing"

func TestEstimateTokens(t *testing.T) {
	cases := map[string]int{
		"":      0,
		"a":     1,
		"abcd":  1,
		"abcde": 2,
	}
	for in, want := range cases {
		if got := EstimateTokens(in); got != want {
			t.Errorf("EstimateTokens(%q) = %d, want %d", in, got, want)
		}
	}
}
