// Package version tells which version of usage-control is running.
package version

// Version is the version of this program: "1.2.3" for a release, the commit
// for a build of main, and "dev" for a build that did not set it. Builds set
// it with
//
//	go build -ldflags="-X github.com/Chriszly/usage-control/backend/internal/version.Version=1.2.3"
var Version = "dev"
