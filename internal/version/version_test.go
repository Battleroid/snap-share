package version

import "testing"

func TestBuild(t *testing.T) {
	defer func(v, c string) { Version, Commit = v, c }(Version, Commit)
	Version, Commit = "v0.2.0", "756e887"
	i := Build()
	if i.VersionURL != repoURL+"/releases/tag/v0.2.0" || i.CommitURL != repoURL+"/commit/756e887" {
		t.Errorf("links: %+v", i)
	}
	Version, Commit = "dev", "unknown"
	if i := Build(); i.VersionURL != "" || i.CommitURL != "" {
		t.Errorf("dev build must not link: %+v", i)
	}
	Version, Commit = "v0.2.0-3-gabc-dirty", "\"><x"
	if i := Build(); i.VersionURL != "" || i.CommitURL != "" {
		t.Errorf("odd values must not link: %+v", i)
	}
}
