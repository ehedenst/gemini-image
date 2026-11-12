package cmd

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveValue(t *testing.T) {
	ctx := context.Background()
	for in, want := range map[string]string{
		"plain":               "plain",
		"":                    "",
		"!echo ' secret '":    "secret",
		"!printf 'a b\\n\\n'": "a b",
	} {
		got, err := resolveValue(ctx, nil, io.Discard, in)
		if err != nil || got != want {
			t.Errorf("resolveValue(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := resolveValue(ctx, nil, io.Discard, "!exit 3"); err == nil {
		t.Error("expected error from failing command")
	}
}

func TestLoadEnvFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), ".env")
	if err := loadEnvFile(missing, false); err != nil {
		t.Errorf("optional missing file: %v", err)
	}
	if err := loadEnvFile(missing, true); err == nil {
		t.Error("required missing file: expected error")
	}

	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("GI_TEST_A=\"!echo hi\"\nGI_TEST_B=fromfile\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GI_TEST_A", "")
	os.Unsetenv("GI_TEST_A")
	t.Setenv("GI_TEST_B", "fromenv")
	if err := loadEnvFile(path, true); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("GI_TEST_A"); got != "!echo hi" {
		t.Errorf("GI_TEST_A = %q", got)
	}
	if got := os.Getenv("GI_TEST_B"); got != "fromenv" {
		t.Errorf("existing env should win, GI_TEST_B = %q", got)
	}
}
