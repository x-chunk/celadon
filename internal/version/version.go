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

// Info is the build description, filled from the link-time variables and,
// for a `go install` build that has none, from the module's build info.
type Info struct {
	Version string
	Commit  string
	Date    string
	Go      string
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
	return fmt.Sprintf("%s %s/%s %s", s, runtime.GOOS, runtime.GOARCH, i.Go)
}
