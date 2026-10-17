// Package chunk splits parsed symbols into embeddable chunks, each with a
// content hash and token count.
//
// This version gives every struct and interface exactly one chunk, groups a
// type with its same-file constructor as one chunk when the pair is small, and
// splits every function and method into one chunk with its leading doc comment,
// exact line span and lexical text, or into parts at top-level statement
// boundaries when it is over the ceiling. Context headers come later (D-078,
// D-081, D-083).
package chunk
