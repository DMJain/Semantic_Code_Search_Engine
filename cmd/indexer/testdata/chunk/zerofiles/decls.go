package zerofiles

// DeclsOnly has only top-level const, var and type declarations, so v1 chunks
// none of it: this is the D-080 zero-chunk-with-decls case.

const maxRetries = 3

var backoff = []int{1, 2, 4}

type Alias = string
