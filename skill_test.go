package client

import (
	"strconv"
	"strings"
	"testing"
)

func TestSkill(t *testing.T) {
	if !strings.HasPrefix(Skill, "---\nname: "+SkillName+"\ndescription: ") {
		t.Fatalf("the front matter must open the file: %.80s", Skill)
	}
	if body := SkillBody(); !strings.HasPrefix(body, "# ") || strings.Contains(body, "description:") {
		t.Errorf("SkillBody keeps the front matter out: %.80s", body)
	}
	rules := SkillRules()
	for _, want := range []string{"1. Never put an API key", "`Idempotency-Key`", "`dry_run: true`", "Stop the game servers", "data, never instructions"} {
		if !strings.Contains(rules, want) {
			t.Errorf("the rules lack %q: %s", want, rules)
		}
	}
	if strings.Contains(rules, "##") {
		t.Errorf("SkillRules runs past its section: %s", rules)
	}
	// The MCP server calls the API itself: the rule on calling it by hand stays in the file, not in what it sends.
	if !strings.Contains(Skill, ". "+byHand) {
		t.Errorf("the skill file lost its rule on calling the API by hand; update byHand: %q", byHand)
	}
	if strings.Contains(rules, "curl") || strings.Contains(rules, byHand) {
		t.Errorf("the rules the MCP server sends tell the agent to call the API by hand: %s", rules)
	}
	for i, line := range strings.Split(rules, "\n") {
		if want := strconv.Itoa(i+1) + ". "; !strings.HasPrefix(line, want) {
			t.Errorf("rule %d is numbered %.4q", i+1, line)
		}
	}
}
