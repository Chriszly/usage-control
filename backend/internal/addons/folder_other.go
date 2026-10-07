//go:build !windows

package addons

// checkFolder accepts every folder: only on Windows does an add-on run with
// more rights than the account that may change its folder.
func checkFolder(string) error {
	return nil
}
