package main

import "runtime/debug"

// version can be set at build time with -ldflags "-X main.version=v1.2.3".
// Left empty, the version Go recorded in the binary is used instead, which is
// the release tag for anything installed with `go install ...@version`.
var version string

func currentVersion() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}
