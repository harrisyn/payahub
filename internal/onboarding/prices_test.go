package onboarding

import (
	"os"
	"regexp"
	"strconv"
	"testing"
)

// The web app shows plan prices from its own file; they must match the
// prices the ledger charges.
func TestWebShowsTheChargedPrices(t *testing.T) {
	src, err := os.ReadFile("../../apps/web/lib/pricing.ts")
	if err != nil {
		t.Skip("web app not present:", err)
	}
	re := regexp.MustCompile(`id: "(\w+)",\s*name: "[^"]*",\s*monthlyFee: (null|[\d.]+),\s*perClaim: (null|[\d.]+),\s*included: (\d+)`)
	minor := func(s string) int64 {
		if s == "null" {
			return 0
		}
		f, _ := strconv.ParseFloat(s, 64)
		return int64(f*100 + 0.5)
	}
	found := 0
	for _, m := range re.FindAllStringSubmatch(string(src), -1) {
		p, ok := Prices[Plan(m[1])]
		if !ok {
			continue
		}
		found++
		inc, _ := strconv.Atoi(m[4])
		if got := (Price{MonthlyFee: minor(m[2]), PerClaim: minor(m[3]), Included: inc}); got != p {
			t.Errorf("%s: web shows %+v, the ledger charges %+v", m[1], got, p)
		}
	}
	if found != len(Prices) {
		t.Fatalf("found %d of %d plans in pricing.ts", found, len(Prices))
	}
}
