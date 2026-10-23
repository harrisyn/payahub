package deid

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

func TestTextKeepsTheClinicalPicture(t *testing.T) {
	dob := time.Date(1988, 6, 14, 0, 0, 0, 0, time.UTC)
	in := `Patient Name: Abena Owusu
NHIS No: 4821 1907
Ghana Card GHA-123456789-0, tel +233 24 455 6677, or 0244556677, abena.owusu@example.com
Address: H/No. 14 Ring Road, Osu
Lives at GA-183-8164. Born 14/06/1988.
Age 38, female. Diagnosis: uncomplicated malaria (B54). Malaria RDT positive; Hb 11.2 g/dL.
Bill: OPD consultation 150.00, Artemether/Lumefantrine 80/480 x6 45.00, total GHS 1,239.00. Seen 03/10/2026.
Owusu was advised to return.`
	out, c := Text(in, Identity{Names: []string{"Abena Owusu"}, Numbers: []string{"48211907"}, BirthDate: &dob})
	for _, gone := range []string{"Abena", "Owusu", "4821", "GHA-123", "455 6677", "0244556677", "example.com", "Ring Road", "GA-183", "14/06/1988"} {
		if strings.Contains(out, gone) {
			t.Errorf("%q is still there:\n%s", gone, out)
		}
	}
	for _, kept := range []string{"Age 38, female", "uncomplicated malaria (B54)", "Hb 11.2 g/dL", "150.00", "80/480", "1,239.00", "03/10/2026"} {
		if !strings.Contains(out, kept) {
			t.Errorf("%q was removed:\n%s", kept, out)
		}
	}
	if !c.Covers([]byte(out)) || c.Covers([]byte(in)) {
		t.Error("Cleared must cover the scrubbed text and only that")
	}
	if c.Report.Removed["name"] < 2 || c.Report.Removed["phone"] < 2 {
		t.Errorf("report: %+v", c.Report)
	}
}

func TestCatalogueKeepsCodes(t *testing.T) {
	out, _ := Catalogue("MED-0001 | medication | Paracetamol 500 mg tablet\n6001234567890 | Amoxicillin 250mg\nask jane@clinic.com")
	if !strings.Contains(out, "6001234567890") || !strings.Contains(out, "MED-0001") || strings.Contains(out, "jane@") {
		t.Errorf("catalogue: %s", out)
	}
}

func TestZeroClearedCoversNothing(t *testing.T) {
	var c Cleared
	if c.Covers(nil) || c.Covers([]byte("x")) {
		t.Fatal("zero Cleared covered something")
	}
	_, a := Catalogue("a")
	_, b := Catalogue("b")
	if j := Join(a, b); !j.Covers([]byte("a")) || !j.Covers([]byte("b")) || j.Covers([]byte("c")) {
		t.Fatal("join")
	}
}

func TestAge(t *testing.T) {
	b := time.Date(1988, 6, 14, 0, 0, 0, 0, time.UTC)
	if a := Age(b, time.Date(2026, 6, 13, 0, 0, 0, 0, time.UTC)); a != 37 {
		t.Errorf("day before birthday: %d", a)
	}
	if a := Age(b, time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC)); a != 38 {
		t.Errorf("birthday: %d", a)
	}
}

// With Tesseract installed, a typed form is read and redacted for real.
func TestRedactWithTesseract(t *testing.T) {
	if _, err := exec.LookPath("tesseract"); err != nil {
		t.Skip("no tesseract")
	}
	page := typed(
		"OUTPATIENT CLAIM FORM",
		"Patient Name: Kwame Mensah   Age: 34   Sex: M",
		"NHIS No: 48211907",
		"Tel: 024 455 6677",
		"Diagnosis: Malaria B54",
		"Consultation 1 120.00",
		"Kwame to return in 3 days",
	)
	r, err := Redact(context.Background(), Local{}, page, "image/png", Identity{})
	if err != nil {
		t.Fatal(err)
	}
	if out := os.Getenv("DEID_OUT"); out != "" {
		_ = os.WriteFile(out+".redacted.png", r.Pages[0], 0o644)
	}
	if r.Found["name"] != "Kwame Mensah" || r.Found["member_number"] != "48211907" {
		t.Errorf("found: %v", r.Found)
	}
	words, err := Local{}.Words(context.Background(), mustPNG(t, r.Pages[0]))
	if err != nil {
		t.Fatal(err)
	}
	var seen []string
	for _, w := range words {
		seen = append(seen, w.Text)
	}
	text := strings.Join(seen, " ")
	for _, gone := range []string{"Kwame", "Mensah", "48211907", "6677"} {
		if strings.Contains(text, gone) {
			t.Errorf("%q can still be read: %s", gone, text)
		}
	}
	for _, kept := range []string{"Age", "34", "Malaria", "120.00"} {
		if !strings.Contains(text, kept) {
			t.Errorf("%q was lost: %s", kept, text)
		}
	}
	if _, err := Redact(context.Background(), Local{}, typed("Malaria B54", "Consultation 120.00"), "image/png", Identity{}); !errors.Is(err, ErrNameNotFound) {
		t.Errorf("a form with no name label: %v", err)
	}
}

// typed draws lines of text in Go Regular at a scan's size.
func typed(lines ...string) []byte {
	f, _ := opentype.Parse(goregular.TTF)
	face, _ := opentype.NewFace(f, &opentype.FaceOptions{Size: 30, DPI: 72})
	img := image.NewRGBA(image.Rect(0, 0, 1100, 40+len(lines)*52))
	draw.Draw(img, img.Bounds(), image.White, image.Point{}, draw.Src)
	d := font.Drawer{Dst: img, Src: image.NewUniform(color.Black), Face: face}
	for i, l := range lines {
		d.Dot = fixed.P(30, 50+i*52)
		d.DrawString(l)
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	if out := os.Getenv("DEID_OUT"); out != "" {
		_ = os.WriteFile(out, b.Bytes(), 0o644)
	}
	return b.Bytes()
}

func mustPNG(t *testing.T, b []byte) image.Image {
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func TestOneEdit(t *testing.T) {
	for _, c := range []struct {
		a, b string
		ok   bool
	}{{"mensah", "mensah", true}, {"mensah", "mensa", true}, {"mensah", "rnensah", false}, {"kwame", "kweme", true}, {"kwame", "kueme", false}, {"owusu", "owusuu", true}} {
		if oneEdit(c.a, c.b) != c.ok {
			t.Errorf("%s %s", c.a, c.b)
		}
	}
}
