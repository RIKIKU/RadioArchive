// Package version is the version of the API and the recorder client, which
// are released together.
package version

// Version is set at build time with -ldflags "-X .../internal/version.Version=..."
// for a release.
var Version = "0.1.0-dev"
