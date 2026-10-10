package testdb

import (
	"context"
	"strings"
	"testing"
)

func TestNameFitsPostgresLimitAndKeepsPrefixAndSuffix(t *testing.T) {
	long := strings.Repeat("VeryLongPackageName-", 10)
	got := Name(long, "deadbeef")
	if len(got) > 63 {
		t.Errorf("len(%q) = %d, want <= 63", got, len(got))
	}
	if !strings.HasPrefix(got, Prefix) {
		t.Errorf("%q lacks prefix %q", got, Prefix)
	}
	if !strings.HasSuffix(got, "_deadbeef") {
		t.Errorf("%q lost its random suffix", got)
	}
	if got != strings.ToLower(got) {
		t.Errorf("%q is not lowercase", got)
	}
}

func TestNameSanitizesPackage(t *testing.T) {
	if got, want := Name("My-Pkg.v2", "0a1b2c3d"), "codesearch_test_my_pkg_v2_0a1b2c3d"; got != want {
		t.Errorf("Name = %q, want %q", got, want)
	}
}

func TestDropRefusesForeignName(t *testing.T) {
	// A nil connection is fine: the prefix check must fail before it is used.
	err := drop(context.Background(), nil, "codesearch")
	if err == nil || !strings.Contains(err.Error(), "refusing to drop") {
		t.Errorf("drop(codesearch) error = %v, want refusal", err)
	}
}
