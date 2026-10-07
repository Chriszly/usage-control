package sysfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTextAndUint(t *testing.T) {
	dir := t.TempDir()
	write := func(name, text string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	number := write("number", "1500000\n")
	word := write("word", " Battery\n")
	missing := filepath.Join(dir, "missing")

	if got := Text(word); got != "Battery" {
		t.Errorf("Text = %q, want Battery", got)
	}
	if got := Text(missing); got != "" {
		t.Errorf("Text of a missing file = %q, want empty", got)
	}
	if n, ok := Uint(number); !ok || n != 1500000 {
		t.Errorf("Uint = %d, %v, want 1500000, true", n, ok)
	}
	for _, path := range []string{word, missing} {
		if _, ok := Uint(path); ok {
			t.Errorf("Uint(%s) is ok, want false", filepath.Base(path))
		}
	}
}
