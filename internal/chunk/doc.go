// Package chunk splits parsed symbols into embeddable chunks, each with a
// context header, content hash and token count.
//
// This version splits every function and method into one chunk with its
// leading doc comment, exact line span and lexical text; struct, interface and
// group chunks, parts and headers come later (D-078, D-081, D-083).
package chunk
