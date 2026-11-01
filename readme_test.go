package einvoice

import (
	"os"
	"regexp"
	"strconv"
	"testing"
)

// README.md quotes the size of the rule set next to the link to the live page
// that lists it. Prose rots: this test makes the numbers in it the numbers this
// build actually enforces, so a rule-set bump cannot leave the README (and the
// page it points at) promising something else.
func TestREADMEStatesTheRuleCountsThisBuildEnforces(t *testing.T) {
	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	ids, assertions := ruleSetSize(t)
	const want = `([\d,]+) rule ids, ([\d,]+)\s+assertions`
	found := regexp.MustCompile(want).FindStringSubmatch(string(readme))
	if found == nil {
		t.Fatalf("README no longer states its rule counts as %q", want)
	}
	if got, expect := found[1], withThousands(ids); got != expect {
		t.Errorf("README says %s rule ids, this build enforces %s", got, expect)
	}
	if got, expect := found[2], withThousands(assertions); got != expect {
		t.Errorf("README says %s assertions, this build enforces %s", got, expect)
	}
}

// ruleSetSize counts what a plain Validate call applies to the two official
// syntaxes: the assertions in both Schematron sets, and the distinct rule ids
// they share.
func ruleSetSize(t *testing.T) (ids, assertions int) {
	t.Helper()
	seen := map[string]bool{}
	for _, sx := range []Syntax{SyntaxCII, SyntaxUBL} {
		list, err := Rules(sx, Options{})
		if err != nil {
			t.Fatalf("Rules(%s): %v", sx, err)
		}
		assertions += len(list)
		for _, r := range list {
			if !seen[r.ID] {
				seen[r.ID] = true
				ids++
			}
		}
	}
	return ids, assertions
}

func withThousands(n int) string {
	digits := strconv.Itoa(n)
	for i := len(digits) - 3; i > 0; i -= 3 {
		digits = digits[:i] + "," + digits[i:]
	}
	return digits
}
