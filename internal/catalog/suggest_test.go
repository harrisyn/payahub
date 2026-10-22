package catalog

import "testing"

func TestSuggestPayerCodesNeedsAWordThatNamesTheItem(t *testing.T) {
	d := Seed()
	d.Lists = map[string][]ListEntry{"glico": {
		{Code: "D6", Name: "Amlodipine 10mg Tab"},
		{Code: "D7", Name: "Ibuprofen 400mg Tab"},
		{Code: "D2", Name: "Amoxicillin 500mg Caps"},
		{Code: "L8", Name: "Widal Test"},
		{Code: "C1", Name: "GP Consultation"},
		{Code: "C2", Name: "Specialist Consultation"},
	}}
	c, err := Build(d)
	if err != nil {
		t.Fatal(err)
	}
	old := get()
	Use(c)
	defer Use(old)

	for item, want := range map[string]string{
		"MED-0016": "", // cetirizine 10 mg: only the strength and form are shared
		"MED-0008": "", // metronidazole 400 mg
		"LAB-0009": "", // pregnancy test
		"SRV-0006": "", // dental consultation
		"MED-0002": "D2",
		"SRV-0002": "C2",
	} {
		got := SuggestPayerCodes("glico", item, 3)
		switch {
		case want == "" && len(got) > 0:
			t.Errorf("%s: suggested %s (%s), want nothing", item, got[0].Code, got[0].Name)
		case want != "" && (len(got) == 0 || got[0].Code != want):
			t.Errorf("%s: got %v, want %s first", item, got, want)
		}
	}
	if got := Suggest("Cetirizine 10mg tab", "", 3); len(got) > 0 && got[0].Item.ID != "MED-0016" {
		t.Errorf("Suggest cetirizine: got %s first", got[0].Item.ID)
	}
}
