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

// Every standard item is found first by its own name and by each of its
// synonyms, so a clinic writing any of them gets the right suggestion.
func TestEveryItemFindsItself(t *testing.T) {
	for _, it := range Items("") {
		for _, q := range append([]string{it.Name}, it.Synonyms...) {
			got := Suggest(q, "", 2)
			if len(got) == 0 || got[0].Item.ID != it.ID {
				t.Errorf("%q (%s): got %v", q, it.ID, ids(got))
				continue
			}
			// Two strengths of one medicine tie when the query names no strength.
			if len(got) > 1 && got[1].Confidence >= got[0].Confidence && got[1].Item.Generic != it.Generic {
				t.Errorf("%q (%s): tie with %s at %.2f", q, it.ID, got[1].Item.ID, got[1].Confidence)
			}
		}
	}
}

func ids(ss []Suggestion) []string {
	var out []string
	for _, s := range ss {
		out = append(out, s.Item.ID+" "+s.Item.Name)
	}
	return out
}

// Names as they appear on Ghanaian clinic price lists, and what they are.
func TestClinicNames(t *testing.T) {
	for q, want := range map[string]string{
		"Coartem 80/480 tabs":         "Artemether and lumefantrine 80/480 mg tablet",
		"Augmentin 625mg":             "Amoxicillin and clavulanic acid 625 mg tablet",
		"Inj. Ceftriaxone 1g":         "Ceftriaxone 1 g injection",
		"Inj Ceftriaxone 500mg":       "Ceftriaxone 500 mg injection",
		"Normal Saline 500ml":         "Sodium chloride 0.9% 500 ml infusion",
		"IV Flagyl 500mg":             "Metronidazole 500 mg/100 ml infusion",
		"Septrin tabs":                "Sulfamethoxazole and trimethoprim 800/160 mg tablet",
		"Hb Electrophoresis":          "Haemoglobin electrophoresis",
		"HBsAg":                       "Hepatitis B surface antigen",
		"Stool R/E":                   "Stool routine examination",
		"Urine C/S":                   "Urine culture and sensitivity",
		"BUE & Cr":                    "Kidney function tests",
		"Pelvic Scan":                 "Ultrasound, pelvic",
		"CT Brain":                    "CT scan, head",
		"Caesarean Section":           "Caesarean section",
		"Private Ward (per day)":      "Ward admission, private room, per day",
		"Paediatric Consultation":     "Consultation, paediatrician",
		"Mixtard 30/70":               "Insulin human, biphasic 30/70 100 IU/ml injection",
		"Depo-Provera inj":            "Medroxyprogesterone 150 mg/ml injection",
		"Paracetamol Syrup 120mg/5ml": "Paracetamol 120 mg/5 ml syrup",
	} {
		got := Suggest(q, "", 1)
		if len(got) == 0 || got[0].Item.Name != want {
			t.Errorf("%q: got %v, want %s", q, ids(got), want)
		}
	}
}
