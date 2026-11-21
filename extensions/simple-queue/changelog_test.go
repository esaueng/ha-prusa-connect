package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// versionHeading is a release heading of CHANGELOG.md: a version and the date
// it was released on.
var versionHeading = regexp.MustCompile(`(?m)^## ([0-9]+\.[0-9]+\.[0-9]+) - ([0-9]{4}-[0-9]{2}-[0-9]{2})$`)

// TestTheChangelogNamesTheVersionThisModuleIs. An operator installs a release
// and reads the changelog to decide whether to, so a changelog whose newest
// entry is not the version being shipped describes another build.
func TestTheChangelogNamesTheVersionThisModuleIs(t *testing.T) {
	t.Parallel()

	page, err := os.ReadFile("CHANGELOG.md")
	if err != nil {
		t.Fatalf("read CHANGELOG.md: %v", err)
	}

	found := versionHeading.FindAllStringSubmatch(strings.ReplaceAll(string(page), "\r\n", "\n"), -1)
	if len(found) == 0 {
		t.Fatal("CHANGELOG.md carries no release heading of the form '## 1.0.0 - 2026-09-21'")
	}

	newest := found[0][1]

	if newest != string(release) {
		t.Errorf("the newest entry of CHANGELOG.md is %s and this process reports %s", newest, release)
	}

	if declared := scalar(manifest(t), "version"); declared != newest {
		t.Errorf("module.yaml declares version %s and the newest entry of CHANGELOG.md is %s",
			declared, newest)
	}
}

// TestEveryChangelogEntryIsUnderACategory: a bullet with no heading over it is
// a bullet a reader cannot place.
func TestEveryChangelogEntryIsUnderACategory(t *testing.T) {
	t.Parallel()

	page, err := os.ReadFile("CHANGELOG.md")
	if err != nil {
		t.Fatalf("read CHANGELOG.md: %v", err)
	}

	categories := map[string]bool{
		"### Security": true, "### Removed": true, "### Changed": true,
		"### Deprecated": true, "### Added": true, "### Fixed": true,
	}

	under := ""

	for line := range strings.SplitSeq(strings.ReplaceAll(string(page), "\r\n", "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "## "):
			under = ""
		case strings.HasPrefix(line, "### "):
			if !categories[strings.TrimSpace(line)] {
				t.Errorf("%q is not one of the categories a reader scans by", line)
			}

			under = strings.TrimSpace(line)
		case strings.HasPrefix(line, "- ") && under == "":
			t.Errorf("the bullet %q stands under no category", line)
		}
	}
}
