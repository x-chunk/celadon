// Package version reports which build of celadon is running.
package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

// These are set at link time by the release build:
//
//	-ldflags "-X github.com/x-chunk/celadon/internal/version.Version=v1.2.3"
var (
	Version = "dev"
	Commit  = ""
	Date    = ""
)

// tealModule is the client every call to the API goes through, whose
// version a report of the build names.
const tealModule = "go.xchunk.org/teal"

// Info is the build description, filled from the link-time variables and,
// for a `go install` build that has none, from the module's build info.
type Info struct {
	Version string
	Commit  string
	Date    string
	Go      string
	// Teal is the version of teal the binary was built with, empty when the
	// build info does not say.
	Teal string
}

// Get returns the description of the running binary.
func Get() Info {
	info := Info{Version: Version, Commit: Commit, Date: Date, Go: runtime.Version()}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return info
	}
	if info.Version == "dev" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		info.Version = bi.Main.Version
	}
	info.Teal = depVersion(bi, tealModule)
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			if info.Commit == "" {
				info.Commit = s.Value
			}
		case "vcs.time":
			if info.Date == "" {
				info.Date = s.Value
			}
		}
	}
	return info
}

// depVersion is the version of a module the binary was built with, or of
// what replaced it; empty when the binary does not hold it.
func depVersion(bi *debug.BuildInfo, path string) string {
	for _, dep := range bi.Deps {
		if dep.Path != path {
			continue
		}
		if dep.Replace != nil {
			return dep.Replace.Version
		}
		return dep.Version
	}
	return ""
}

// String renders the description on one line, the way `celadon version`
// prints it.
func (i Info) String() string {
	s := "celadon " + i.Version
	if i.Commit != "" {
		commit := i.Commit
		if len(commit) > 12 {
			commit = commit[:12]
		}
		s += " (" + commit
		if i.Date != "" {
			s += ", " + i.Date
		}
		s += ")"
	}
	if i.Teal != "" {
		s += " teal " + i.Teal
	}
	return fmt.Sprintf("%s %s/%s %s", s, runtime.GOOS, runtime.GOARCH, i.Go)
}
