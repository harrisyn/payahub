package api

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every write a member can reach is given to a permission on purpose. A new
// route that is not is refused to every member, and this test says so.
func TestEveryConsoleWriteHasAPermission(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	route := regexp.MustCompile(`mux\.Handle\("([A-Z]+) ([^"]+)", s\.(auth|session)\(`)
	seen, routes := 0, map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range route.FindAllStringSubmatch(string(src), -1) {
			seen++
			pattern := m[1] + " " + m[2]
			routes[pattern] = true
			if m[1] == "GET" || anyone[pattern] {
				continue
			}
			if _, ok := writes[pattern]; !ok {
				t.Errorf("%s (in %s) has no permission in writes", pattern, f)
			}
		}
	}
	if seen < 30 {
		t.Fatalf("found only %d routes; the pattern no longer matches how routes are written", seen)
	}
	for p := range writes {
		if strings.HasPrefix(p, "GET ") {
			t.Errorf("%s: reads are open to every member", p)
		}
		if !routes[p] {
			t.Errorf("%s is in writes but is not a route", p)
		}
	}
}
