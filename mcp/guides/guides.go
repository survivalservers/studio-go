// Package guides holds the written docs pages an agent needs to set a game up, as Markdown copies of the docs
// site's own pages (made by the docs build, never edited here), with a guided setup per engine and a small search.
package guides

import (
	"embed"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"unicode"
)

//go:embed pages
var files embed.FS

// Page is one docs page.
type Page struct {
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Description string `json:"description"`
	URL         string `json:"url"`
	Body        string `json:"-"`
}

// Manifest is pages/manifest.json, written with the pages.
type Manifest struct {
	DocsURL string `json:"docs_url"`
	Pages   []Page `json:"pages"`
}

var manifest = mustLoad()

func mustLoad() Manifest {
	raw, err := files.ReadFile("pages/manifest.json")
	if err != nil {
		panic(err)
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		panic(err)
	}
	for i, p := range m.Pages {
		body, err := files.ReadFile("pages/" + p.Slug + ".md")
		if err != nil {
			panic(fmt.Sprintf("guides: %s is in the manifest but has no page: %v", p.Slug, err))
		}
		m.Pages[i].Body = string(body)
	}
	return m
}

// All returns every page, in the docs' order.
func All() []Page { return slices.Clone(manifest.Pages) }

// DocsURL is the docs site's address.
func DocsURL() string { return manifest.DocsURL }

// Get returns one page by slug.
func Get(slug string) (Page, bool) {
	for _, p := range manifest.Pages {
		if p.Slug == slug {
			return p, true
		}
	}
	return Page{}, false
}

// Engines are the engines with a guided setup.
var Engines = []string{"unreal", "unity", "godot", "custom"}

// Setup returns the pages for one engine and system, in reading order: the engine's start page, then how a build
// for that system is made and uploaded, the local interface and testing on your own computer, with the persistent
// server and backup pages when the game keeps its saves. Pages not written yet are named in missing.
func Setup(engine, os string, persistent bool) (pages []Page, missing []string, err error) {
	start := map[string]string{
		"unreal": "get-started/unreal",
		"unity":  "get-started/unity",
		"godot":  "get-started/godot",
		"custom": "get-started/quick-start",
	}[engine]
	if start == "" {
		return nil, nil, fmt.Errorf("no guided setup for engine %q; use one of %s (custom covers every other engine)", engine, strings.Join(Engines, ", "))
	}
	build := map[string]string{"windows": "game-server/windows-builds", "linux": "game-server/linux-images"}[os]
	if build == "" {
		return nil, nil, fmt.Errorf("os must be windows or linux, not %q", os)
	}
	slugs := []string{start}
	if engine == "custom" {
		slugs = append(slugs, "game-server/environment-and-allocation")
	}
	slugs = append(slugs, build, "guides/upload-builds", "game-server/local-api", "game-server/test-locally")
	if persistent {
		slugs = append(slugs, "concepts/persistent-servers", "guides/back-up-and-restore")
	} else {
		slugs = append(slugs, "concepts/ready-servers")
	}
	for _, s := range slugs {
		if p, ok := Get(s); ok {
			pages = append(pages, p)
		} else {
			missing = append(missing, s)
		}
	}
	return pages, missing, nil
}

// Hit is one search result: a section of a page.
type Hit struct {
	Page    Page
	Heading string
	Text    string
	Score   int
}

// Search finds the page sections (split at ## headings) that best match the words in query.
func Search(query string, limit int) []Hit {
	words := terms(query)
	if len(words) == 0 {
		return nil
	}
	var hits []Hit
	for _, p := range manifest.Pages {
		for _, sec := range sections(p) {
			low := strings.ToLower(sec.Heading + "\n" + sec.Text)
			title := strings.ToLower(p.Title + " " + sec.Heading)
			score, matched := 0, 0
			for _, w := range words {
				n := strings.Count(low, w)
				if n > 0 {
					matched++
					score += min(n, 5)
					if strings.Contains(title, w) {
						score += 5
					}
				}
			}
			if matched == 0 {
				continue
			}
			// Sections that match more of the words rank first.
			score += matched * 10
			hits = append(hits, Hit{Page: p, Heading: sec.Heading, Text: sec.Text, Score: score})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

type section struct{ Heading, Text string }

func sections(p Page) []section {
	var out []section
	cur := section{Heading: p.Title}
	var b strings.Builder
	inCode := false
	for _, line := range strings.Split(p.Body, "\n") {
		if strings.HasPrefix(line, "```") {
			inCode = !inCode
		}
		if !inCode && strings.HasPrefix(line, "## ") {
			cur.Text = strings.TrimSpace(b.String())
			if cur.Text != "" {
				out = append(out, cur)
			}
			cur, b = section{Heading: strings.TrimPrefix(line, "## ")}, strings.Builder{}
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	cur.Text = strings.TrimSpace(b.String())
	if cur.Text != "" {
		out = append(out, cur)
	}
	return out
}

var stop = map[string]bool{"the": true, "a": true, "an": true, "to": true, "of": true, "and": true, "or": true, "in": true,
	"on": true, "for": true, "is": true, "my": true, "how": true, "do": true, "i": true, "what": true, "with": true, "it": true,
	"can": true, "does": true, "be": true, "from": true, "your": true, "you": true}

func terms(q string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(strings.ToLower(q), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-'
	}) {
		if len(f) > 1 && !stop[f] && !slices.Contains(out, f) {
			out = append(out, f)
		}
	}
	return out
}
