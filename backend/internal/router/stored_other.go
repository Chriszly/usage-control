//go:build !windows

package router

// StoredPasswords returns the router passwords an installer stored for the
// service; only the Windows installer stores them, elsewhere they are in the
// passwords file (see PasswordsPath).
func StoredPasswords(string) (map[string]string, bool, error) {
	return nil, false, nil
}
