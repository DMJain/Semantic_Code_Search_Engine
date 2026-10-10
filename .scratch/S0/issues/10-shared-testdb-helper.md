# 10: Shared throwaway-database test helper (S0 follow-up)

**What to build:** `internal/testdb` gives every Postgres integration test its own migrated throwaway database (`codesearch_test_<pkg>_<8hex>`, at most 63 bytes), dropped on cleanup, so tests never write to the dev database and can run in parallel across packages. An empty `TEST_DATABASE_URL` skips the test, except when `REQUIRE_TEST_DB=1` (set by `make test` and CI), where it fails. The helper refuses to drop any name without the `codesearch_test_` prefix. The raw `t.Skip` in the store test is replaced by the helper. CI runs one `go test -v -count=1 ./...` with the SKIP grep over every package (the store-only second run is removed). `make test-db-clean` drops leftover `codesearch_test_*` databases. D-060 is moved above D-061 (order only, no text change).

**Blocked by:** S0 tickets 01-09 (done)

**Status:** done

**Numbering:** this is issue 10 of the scratch folder; the sprint file's task S0-T10 (the sign-off run) is issue 09.

**Found by:** S0 code review; needed by S1 (see `.scratch/S1/spec.md`, user stories on DB isolation).

- [x] `make test`: 0 SKIP, `--- PASS` for the three store tests, dev table counts identical before and after
- [x] 0 `codesearch_test_%` databases after the run; `make test-db-clean` removes a planted one
- [x] `REQUIRE_TEST_DB=1` with an empty URL fails the store tests; without it they skip
- [x] Name builder test: long package name stays at most 63 bytes with prefix and suffix intact; drop of a foreign name is refused
- [x] `go vet ./...` and `make lint`: 0 issues
