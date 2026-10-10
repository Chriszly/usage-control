package router

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestDPAPIRoundTrip(t *testing.T) {
	plain := formatPasswords(map[string]string{"Router": "se=cret ", "Box": "x"})
	encrypted, err := protect(plain)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encrypted, []byte("se=cret")) {
		t.Error("the password is in the encrypted data")
	}
	decrypted, err := unprotect(encrypted)
	if err != nil || !bytes.Equal(decrypted, plain) {
		t.Fatalf("got %q, %v", decrypted, err)
	}
}

func TestStoredPasswordsReadsTheEncryptedFile(t *testing.T) {
	dir := t.TempDir()
	encrypted, err := protect(formatPasswords(map[string]string{"Router": "secret"}))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, protectedFile), encrypted, 0o600); err != nil {
		t.Fatal(err)
	}
	passwords, stored, err := StoredPasswords(dir)
	if err != nil || !stored || passwords["Router"] != "secret" {
		t.Errorf("got %v, %v, %v", passwords, stored, err)
	}
	if _, stored, err := StoredPasswords(t.TempDir()); err != nil || stored {
		t.Errorf("an empty folder: %v, %v", stored, err)
	}
}
