// Package version holds the release version, overridable at link time:
//
//	go build -ldflags "-X github.com/zypher-systems/kairos/internal/version.Version=1.18.0"
package version

// Version is the Kairos release. Keep in sync with web/package.json.
var Version = "1.18.0"
