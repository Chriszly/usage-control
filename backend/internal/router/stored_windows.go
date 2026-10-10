package router

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	// storedKey is where the Windows installer puts the router passwords it
	// was given, readable only by the system, administrators and the
	// service's account (see windows/usage-control.wxs).
	storedKey = `SOFTWARE\Usage Control\Router`
	// storedValue holds them as "name=password" lines, until the service
	// moves them into protectedFile.
	storedValue = "Passwords"
	// protectedFile holds the passwords in the data folder, encrypted with
	// DPAPI for the service's account, so only programs running as that
	// account on this PC can read them.
	protectedFile = "router-passwords.dpapi"
)

// StoredPasswords returns the router passwords the Windows installer stored
// for the service, and whether there are any. The installer puts new ones in
// the registry; the first start after it encrypts them with DPAPI, adds them
// to those encrypted before, in dir, the data folder, and deletes them from
// the registry.
func StoredPasswords(dir string) (map[string]string, bool, error) {
	path := filepath.Join(dir, protectedFile)
	passwords := map[string]string{}
	stored := false
	encrypted, err := os.ReadFile(path) //nolint:gosec // the service's own file in its data folder
	switch {
	case err == nil:
		plain, err := unprotect(encrypted)
		if err != nil {
			return nil, false, fmt.Errorf("decrypt %s: %w", path, err)
		}
		if passwords, err = parsePasswords(bytes.NewReader(plain), path); err != nil {
			return nil, false, err
		}
		stored = true
	case !errors.Is(err, os.ErrNotExist):
		return nil, false, err
	}

	key, err := registry.OpenKey(registry.LOCAL_MACHINE, storedKey, registry.QUERY_VALUE|registry.SET_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return passwords, stored, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf(`open HKLM\%s: %w`, storedKey, err)
	}
	defer func() { _ = key.Close() }()
	value, _, err := key.GetStringValue(storedValue)
	if errors.Is(err, registry.ErrNotExist) {
		return passwords, stored, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf(`read HKLM\%s: %w`, storedKey, err)
	}
	added, err := parsePasswords(bytes.NewReader([]byte(value)), `HKLM\`+storedKey)
	if err != nil {
		return nil, false, err
	}
	if len(added) > 0 {
		maps.Copy(passwords, added)
		encrypted, err := protect(formatPasswords(passwords))
		if err != nil {
			return nil, false, fmt.Errorf("encrypt the router passwords: %w", err)
		}
		if err := os.WriteFile(path, encrypted, 0o600); err != nil {
			return nil, false, err
		}
		stored = true
	}
	// Only once they are encrypted; the next start reads them from the file.
	if err := key.DeleteValue(storedValue); err != nil {
		return nil, false, fmt.Errorf(`delete the router passwords from HKLM\%s: %w`, storedKey, err)
	}
	return passwords, stored, nil
}

// formatPasswords writes passwords as parsePasswords reads them.
func formatPasswords(passwords map[string]string) []byte {
	var out bytes.Buffer
	for _, name := range slices.Sorted(maps.Keys(passwords)) {
		out.WriteString(name + "=" + passwords[name] + "\n")
	}
	return out.Bytes()
}

// protect encrypts data with DPAPI for the current account.
func protect(data []byte) ([]byte, error) {
	return dpapi(data, true)
}

// unprotect decrypts what protect encrypted, on the same PC and account.
func unprotect(data []byte) ([]byte, error) {
	return dpapi(data, false)
}

func dpapi(data []byte, encrypt bool) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("nothing to encrypt or decrypt")
	}
	in := windows.DataBlob{Size: uint32(len(data)), Data: &data[0]} //nolint:gosec // at most maxPasswordsBytes
	var out windows.DataBlob
	var err error
	if encrypt {
		err = windows.CryptProtectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	} else {
		err = windows.CryptUnprotectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	}
	if err != nil {
		return nil, err
	}
	defer func() { _, _ = windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data))) }() //nolint:gosec // DPAPI's buffer, freed as it documents
	return bytes.Clone(unsafe.Slice(out.Data, out.Size)), nil                             //nolint:gosec // the buffer DPAPI returned, out.Size bytes long
}
