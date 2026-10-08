package version

import (
	"runtime/debug"
	"strings"
	"testing"
)

func TestDepVersion(t *testing.T) {
	bi := &debug.BuildInfo{Deps: []*debug.Module{
		{Path: "github.com/spf13/cobra", Version: "v1.10.2"},
		{Path: tealModule, Version: "v0.4.0"},
	}}
	if got := depVersion(bi, tealModule); got != "v0.4.0" {
		t.Errorf("depVersion = %q", got)
	}
	bi.Deps[1].Replace = &debug.Module{Path: "../teal", Version: "v0.4.1-local"}
	if got := depVersion(bi, tealModule); got != "v0.4.1-local" {
		t.Errorf("depVersion of a replaced module = %q", got)
	}
	if got := depVersion(bi, "example.com/absent"); got != "" {
		t.Errorf("depVersion of a module not built in = %q", got)
	}
}

func TestStringNamesTeal(t *testing.T) {
	info := Info{Version: "v1.2.3", Go: "go1.27.0", Teal: "v0.4.0"}
	if s := info.String(); !strings.HasPrefix(s, "celadon v1.2.3 teal v0.4.0 ") {
		t.Errorf("String() = %q", s)
	}
	info.Teal = ""
	if s := info.String(); strings.Contains(s, "teal") {
		t.Errorf("String() without teal = %q", s)
	}
}
