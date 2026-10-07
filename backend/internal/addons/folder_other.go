//go:build !windows

package addons

import "os"

// openFolder opens the add-on folder, whatever it is: only on Windows does an
// add-on run with more rights than the account that may change its folder.
func openFolder(dir string) (*os.Root, error) {
	return os.OpenRoot(dir)
}
