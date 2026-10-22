package icd10

import "testing"

func TestCheck(t *testing.T) {
	for in, want := range map[string]Status{
		"B54": Listed, "b50.9": Listed, "B509": Listed, " R50.9 ": Listed,
		"B24":    Listed,   // WHO-only, from who.tsv
		"U07.1":  Listed,   // WHO emergency code
		"B50.88": Category, // not listed, category known
		"B5":     Invalid, "50.9": Invalid, "B5.09": Invalid, "": Invalid,
		"Q999": Listed, "U999": Unknown,
	} {
		if _, got := Check(in); got != want {
			t.Errorf("Check(%q) = %d, want %d", in, got, want)
		}
	}
	if c, _ := Check("b50.9"); c.Code != "B50.9" || c.Title != "Plasmodium falciparum malaria, unspecified" {
		t.Errorf("B50.9 = %+v", c)
	}
}

func TestSearch(t *testing.T) {
	got := Search("falciparum malaria", 5)
	if len(got) == 0 || got[0].Code != "B50" {
		t.Fatalf("words: %v", got)
	}
	got = Search("b50.", 20)
	if len(got) < 3 || got[0].Code != "B50" || got[1].Code != "B50.0" {
		t.Fatalf("prefix: %v", got)
	}
	if got := Search("typhoid fev", 3); len(got) == 0 || got[0].Code[:3] != "A01" {
		t.Fatalf("typhoid: %v", got)
	}
	if got := Search("zzzz", 3); len(got) != 0 {
		t.Fatalf("nothing: %v", got)
	}
}

func TestIs(t *testing.T) {
	for _, s := range []string{"icd-10", "ICD10", "icd_10", "ICD-10-CM"} {
		if !Is(s) {
			t.Errorf("Is(%q) = false", s)
		}
	}
	if Is("payahub") {
		t.Error("payahub is not ICD-10")
	}
}
