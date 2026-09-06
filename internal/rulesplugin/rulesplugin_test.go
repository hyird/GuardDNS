package rulesplugin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
)

func TestSanitizeRulesNormalizesAndFiltersInvalidContent(t *testing.T) {
	rules, cleaned, stats, err := sanitizeRules([]byte(`# operator note
domain:Apple.COM.
full:api.example.com # trailing note
keyword:example
regexp:^api[0-9]+\.example\.com$
full:bad domain
domain:192.0.2.1
unsupported:example
`))
	if err != nil {
		t.Fatal(err)
	}
	wantRules := []string{
		"domain:apple.com",
		"full:api.example.com",
		"keyword:example",
		`regexp:^api[0-9]+\.example\.com$`,
	}
	if strings.Join(rules, "\n") != strings.Join(wantRules, "\n") {
		t.Fatalf("rules = %#v, want %#v", rules, wantRules)
	}
	if got, want := string(cleaned), strings.Join(wantRules, "\n")+"\n"; got != want {
		t.Fatalf("cleaned = %q, want %q", got, want)
	}
	if stats.meaningful != 7 || stats.rejected != 3 {
		t.Fatalf("stats = %+v, want meaningful=7 rejected=3", stats)
	}
}

func TestRuleFileReloadFiltersInvalidRulesAndPreservesLastGoodSet(t *testing.T) {
	path := filepath.Join(t.TempDir(), "direct.txt")
	if err := os.WriteFile(path, []byte("domain:old.example\n"), 0644); err != nil {
		t.Fatal(err)
	}
	rules, err := newRuleFile(zap.NewNop(), &Args{File: path}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer rules.Close()
	assertMatch(t, rules, "old.example", true)

	if err := os.WriteFile(path, []byte("domain:next.example\nfull:bad domain\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := rules.reload(); err != nil {
		t.Fatal(err)
	}
	assertMatch(t, rules, "old.example", false)
	assertMatch(t, rules, "next.example", true)
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(content), "domain:next.example\n"; got != want {
		t.Fatalf("sanitized file = %q, want %q", got, want)
	}

	if err := os.WriteFile(path, []byte("full:still bad\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := rules.reload(); err != nil {
		t.Fatal(err)
	}
	assertMatch(t, rules, "next.example", true)
	content, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(content), "domain:next.example\n"; got != want {
		t.Fatalf("restored file = %q, want %q", got, want)
	}

	if err := os.WriteFile(path, nil, 0644); err != nil {
		t.Fatal(err)
	}
	if err := rules.reload(); err != nil {
		t.Fatal(err)
	}
	assertMatch(t, rules, "next.example", false)
}

func assertMatch(t *testing.T, rules *RuleFile, name string, want bool) {
	t.Helper()
	_, got := rules.Match(name)
	if got != want {
		t.Fatalf("Match(%q) = %t, want %t", name, got, want)
	}
}
