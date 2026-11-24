// Package version holds build information set with -ldflags.
package version

import (
	"regexp"
	"strings"
)

// Version is set at build time: -ldflags "-X github.com/Battleroid/snap-share/internal/version.Version=v1.2.3".
var Version = "dev"

// Commit is the short git SHA of the build, set the same way (...version.Commit=756e887).
var Commit = "unknown"

var releaseTag = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)

const repoURL = "https://github.com/Battleroid/snap-share"

// Info is the build line for the UI and --version output.
type Info struct {
	Version, Commit       string
	VersionURL, CommitURL string // empty when there is nothing to link to
}

// Build returns the running build. A release link needs a vX.Y.Z version and
// a commit link a hex SHA; anything else (dev, git describe output) is plain text.
func Build() Info {
	i := Info{Version: Version, Commit: Commit}
	if releaseTag.MatchString(Version) {
		i.VersionURL = repoURL + "/releases/tag/" + Version
	}
	if Commit != "" && Commit != "unknown" && strings.Trim(Commit, "0123456789abcdef") == "" {
		i.CommitURL = repoURL + "/commit/" + Commit
	}
	return i
}

// String is "v0.2.0 (756e887)", for logs and version output.
func (i Info) String() string { return i.Version + " (" + i.Commit + ")" }
