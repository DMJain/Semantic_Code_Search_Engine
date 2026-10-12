// Package summarize produces function summaries with a local LLM. It covers
// the pure part (prompt, input hash, eligibility, request guards) and the
// database-backed runner with resume and seeded sampling; file- and
// package-level summaries arrive in S5.
package summarize
