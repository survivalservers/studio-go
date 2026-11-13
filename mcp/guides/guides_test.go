package guides

import (
	"strings"
	"testing"
)

func TestEveryEngineHasAWrittenGuide(t *testing.T) {
	for _, engine := range Engines {
		for _, os := range []string{"windows", "linux"} {
			for _, worlds := range []bool{false, true} {
				pages, missing, err := Setup(engine, os, worlds)
				if err != nil || len(missing) > 0 || len(pages) < 6 {
					t.Errorf("%s %s worlds=%v: %d pages, missing %v, %v", engine, os, worlds, len(pages), missing, err)
				}
			}
		}
	}
	if _, _, err := Setup("cryengine", "windows", false); err == nil || !strings.Contains(err.Error(), "custom covers every other engine") {
		t.Errorf("an unknown engine points at custom: %v", err)
	}
}

func TestPagesArePlainMarkdownWithAbsoluteLinks(t *testing.T) {
	for _, p := range All() {
		if !strings.HasPrefix(p.Body, "# ") || strings.Contains(p.Body, "](/") || strings.Contains(p.Body, "{{docs}}") || strings.Contains(p.Body, "{{product}}") {
			t.Errorf("%s is not a finished Markdown copy", p.Slug)
		}
		if !strings.HasPrefix(p.URL, DocsURL()+"/") {
			t.Errorf("%s: %s", p.Slug, p.URL)
		}
	}
}

func TestSearch(t *testing.T) {
	for q, want := range map[string]string{
		"how does my Godot server stop when it ignores SIGTERM": "get-started/godot",
		"crash dumps on windows":                                "game-server/windows-builds",
		"restore a backup of a persistent server":               "guides/back-up-and-restore",
		"allocation file long-poll":                             "game-server/environment-and-allocation",
	} {
		hits := Search(q, 3)
		found := false
		for _, h := range hits {
			found = found || h.Page.Slug == want
		}
		if !found {
			var got []string
			for _, h := range hits {
				got = append(got, h.Page.Slug+"#"+h.Heading)
			}
			t.Errorf("%q: want %s in the top 3, got %v", q, want, got)
		}
	}
	if len(Search("the and of", 3)) != 0 {
		t.Error("only filler words: no hits")
	}
}
