package client

import (
	_ "embed"
	"strconv"
	"strings"
)

// Skill is the one rules file for AI tools, in the Agent Skills format. The command-line tool installs it, the MCP
// server gives its rules to every tool that connects, and the docs site publishes it, so all three say the same.
//
//go:embed skill/survival-servers/SKILL.md
var Skill string

// SkillName is the skill's name and folder.
const SkillName = "survival-servers"

// SkillBody is the file without its front matter, for a repository's AGENTS.md.
func SkillBody() string {
	_, rest, ok := strings.Cut(strings.TrimPrefix(Skill, "---\n"), "\n---\n")
	if !ok {
		return Skill
	}
	return strings.TrimLeft(rest, "\n")
}

// byHand starts the rule on how to call the API yourself. SkillRules leaves it out.
const byHand = "Call the API over HTTPS"

// SkillRules is the numbered list under the file's "## Rules" heading, for the MCP server to give every tool that
// connects. The MCP server makes the calls itself, and a tool signed in through a browser holds no key, so the rule on
// calling the API with curl is left out and the rest are numbered again.
func SkillRules() string {
	_, rest, ok := strings.Cut(Skill, "## Rules\n")
	if !ok {
		return ""
	}
	rules, _, _ := strings.Cut(rest, "\n## ")
	var out []string
	n, skipping := 0, false
	for _, line := range strings.Split(strings.TrimSpace(rules), "\n") {
		num, text, numbered := strings.Cut(line, ". ")
		if _, err := strconv.Atoi(num); !numbered || err != nil {
			if !skipping {
				out = append(out, line) // a rule's next line
			}
			continue
		}
		if skipping = strings.HasPrefix(text, byHand); skipping {
			continue
		}
		n++
		out = append(out, strconv.Itoa(n)+". "+text)
	}
	return strings.Join(out, "\n")
}
