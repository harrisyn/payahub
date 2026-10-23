package deid

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/jpeg" // phones save JPEG
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	_ "golang.org/x/image/webp" // phones save WebP
)

// A form is redacted on this machine before a model sees it: the words on
// each page are read here (Tesseract), the places that hold the patient's
// name, numbers, birth date, phone and address are found from the labels
// beside them and from their shape, and those places are painted black. The
// model gets only the painted pages, as PNG, with nothing of the original
// file (no PDF text layer, no photo metadata).
//
// It fails closed: a form on which no patient-name label can be found is
// not sent at all, because the name could be anywhere on it.

// ErrNoOCR means the machine has no Tesseract (or no pdftoppm for a PDF).
var ErrNoOCR = errors.New("deid: reading forms needs Tesseract (and pdftoppm for PDFs) on the server")

// ErrNameNotFound means no place for the patient's name was found.
var ErrNameNotFound = errors.New("deid: could not find where the patient's name is on this form")

// Word is one word read from a page, with its box in pixels.
type Word struct {
	Text string
	Box  image.Rectangle
	Line string // words with the same Line are on one line
}

// OCR reads the words on a page image.
type OCR interface {
	Words(ctx context.Context, page image.Image) ([]Word, error)
}

// Local reads pages with the Tesseract and pdftoppm programs.
type Local struct {
	Tesseract string // path; empty looks it up
	PDFToPPM  string
}

func (l Local) tool(set, name string) (string, error) {
	if set != "" {
		return set, nil
	}
	p, err := exec.LookPath(name)
	if err != nil {
		return "", ErrNoOCR
	}
	return p, nil
}

func (l Local) Words(ctx context.Context, page image.Image) ([]Word, error) {
	bin, err := l.tool(l.Tesseract, "tesseract")
	if err != nil {
		return nil, err
	}
	var in bytes.Buffer
	if err := png.Encode(&in, page); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, bin, "stdin", "stdout", "-l", "eng", "--psm", "3", "tsv")
	cmd.Stdin = &in
	cmd.Env = append(os.Environ(), "OMP_THREAD_LIMIT=1")
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("deid: tesseract: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	r := csv.NewReader(&out)
	r.Comma, r.LazyQuotes, r.FieldsPerRecord = '\t', true, -1
	var words []Word
	for i := 0; ; i++ {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("deid: tesseract output: %w", err)
		}
		if i == 0 || len(rec) < 12 || rec[0] != "5" || strings.TrimSpace(rec[11]) == "" {
			continue
		}
		n := make([]int, 4)
		for j := range n {
			n[j], _ = strconv.Atoi(rec[6+j])
		}
		words = append(words, Word{Text: strings.TrimSpace(rec[11]), Box: image.Rect(n[0], n[1], n[0]+n[2], n[1]+n[3]),
			Line: rec[2] + "." + rec[3] + "." + rec[4]})
	}
	return words, nil
}

// pages turns a PDF into page images.
func (l Local) pages(ctx context.Context, pdf []byte, max int) ([]image.Image, error) {
	bin, err := l.tool(l.PDFToPPM, "pdftoppm")
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "payahub-form-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	src := filepath.Join(dir, "form.pdf")
	if err := os.WriteFile(src, pdf, 0o600); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, bin, "-r", "150", "-png", "-f", "1", "-l", strconv.Itoa(max), src, filepath.Join(dir, "p"))
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("deid: the PDF could not be opened: %s", strings.TrimSpace(string(out)))
	}
	names, _ := filepath.Glob(filepath.Join(dir, "p-*.png"))
	sort.Strings(names) // pdftoppm pads the page numbers
	var imgs []image.Image
	for _, n := range names {
		f, err := os.Open(n)
		if err != nil {
			return nil, err
		}
		img, err := png.Decode(f)
		f.Close()
		if err != nil {
			return nil, err
		}
		imgs = append(imgs, img)
	}
	if len(imgs) == 0 {
		return nil, errors.New("deid: the PDF has no pages")
	}
	return imgs, nil
}

// Redacted is a form made safe to show a model, and what was read from the
// blacked-out places here, for a person to check.
type Redacted struct {
	Pages   [][]byte          // PNG
	Found   map[string]string // name, first_name, last_name, member_number, policy_number, birth_date, phone, address, email
	Cleared Cleared
}

// MaxPages is how many pages of a form are read.
const MaxPages = 4

// maxSide is the longest side a page is scaled to: enough for a model to
// read handwriting, small enough to send.
const maxSide = 2000

// Redact blacks out the patient's identity on a form (PDF, PNG, JPEG or WebP).
func Redact(ctx context.Context, ocr OCR, data []byte, mediaType string, id Identity) (Redacted, error) {
	var imgs []image.Image
	switch mediaType {
	case "application/pdf":
		l, ok := ocr.(Local)
		if !ok {
			l = Local{}
		}
		var err error
		if imgs, err = l.pages(ctx, data, MaxPages); err != nil {
			return Redacted{}, err
		}
	case "image/png", "image/jpeg", "image/webp":
		img, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			return Redacted{}, fmt.Errorf("deid: the image could not be read: %w", err)
		}
		imgs = []image.Image{img}
	default:
		return Redacted{}, fmt.Errorf("deid: %s is not a form type that can be redacted", mediaType)
	}

	out := Redacted{Found: map[string]string{}}
	rep := Report{Method: "image", Pages: len(imgs)}
	type page struct {
		img   *image.RGBA
		words []Word
		boxes []image.Rectangle
	}
	var pages []page
	named := false
	for _, src := range imgs {
		img := scaled(src)
		words, err := ocr.Words(ctx, img)
		if err != nil {
			return Redacted{}, err
		}
		boxes, found, hasName := labelledRegions(words, img.Bounds())
		named = named || hasName
		for k, v := range found {
			if out.Found[k] == "" {
				out.Found[k] = v
			}
		}
		rep.add("region", len(boxes))
		pages = append(pages, page{img, words, boxes})
	}
	if !named {
		return Redacted{}, ErrNameNotFound
	}
	// Second pass: the name and numbers read beside their labels, and those
	// already known, are blacked out wherever else they appear, as is
	// anything shaped like an identifier.
	known := id
	known.Names = append(slices.Clone(known.Names), out.Found["name"], out.Found["first_name"], out.Found["last_name"])
	known.Numbers = append(slices.Clone(known.Numbers), out.Found["member_number"], out.Found["policy_number"])
	tokens := map[string]bool{}
	for _, t := range nameTokens(known.Names) {
		if len(t) >= 3 {
			tokens[strings.ToLower(t)] = true
		}
	}
	nums := map[string]bool{}
	for _, n := range append(known.Numbers, known.Phones...) {
		if d := digits(n); len(d) >= 6 {
			nums[d] = true
		}
	}
	for i := range pages {
		p := &pages[i]
		for _, w := range p.words {
			t := strings.ToLower(strings.Trim(w.Text, ".,:;()[]\"'"))
			d := digits(w.Text)
			if nameLike(t, tokens) || (len(d) >= 6 && nums[d]) || identifierShaped(w.Text) {
				p.boxes = append(p.boxes, pad(w.Box, 4))
				rep.add("word", 1)
			}
		}
		for _, b := range p.boxes {
			draw.Draw(p.img, b.Intersect(p.img.Bounds()), image.NewUniform(color.Black), image.Point{}, draw.Src)
		}
		var buf bytes.Buffer
		if err := png.Encode(&buf, p.img); err != nil {
			return Redacted{}, err
		}
		out.Pages = append(out.Pages, buf.Bytes())
	}
	out.Cleared = cleared(rep, out.Pages...)
	return out, nil
}

// nameLike reports whether a word is one of the name's words, allowing one
// letter read differently in a longer word (handwriting, a smudge).
func nameLike(w string, tokens map[string]bool) bool {
	if tokens[w] {
		return true
	}
	if len(w) < 5 {
		return false
	}
	for t := range tokens {
		if len(t) >= 5 && oneEdit(w, t) {
			return true
		}
	}
	return false
}

// oneEdit reports whether a and b differ by at most one letter added,
// removed or changed.
func oneEdit(a, b string) bool {
	if len(a) > len(b) {
		a, b = b, a
	}
	if len(b)-len(a) > 1 {
		return false
	}
	i := 0
	for i < len(a) && a[i] == b[i] {
		i++
	}
	if len(a) == len(b) {
		return a[i+min(1, len(a)-i):] == b[i+min(1, len(b)-i):]
	}
	return a[i:] == b[i+1:]
}

func identifierShaped(w string) bool {
	if email.MatchString(w) || ghCard.MatchString(w) || ghPost.MatchString(w) || intlTel.MatchString(w) {
		return true
	}
	if dateShaped.MatchString(w) {
		return false // visit and service dates stay; a known birth date is matched by Identity
	}
	d := digits(w)
	// A long run of digits with no decimal point is a number that names
	// someone (member, card, phone), not an amount.
	return len(d) >= 8 && !strings.ContainsAny(w, ".,")
}

var dateShaped = regexp.MustCompile(`^\d{1,2}[/.-]\d{1,2}[/.-]\d{2,4}[.,;]?$|^\d{4}-\d{2}-\d{2}$`)

func pad(r image.Rectangle, n int) image.Rectangle { return r.Inset(-n) }

func scaled(src image.Image) *image.RGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	f := 1.0
	if s := max(w, h); s > maxSide {
		f = float64(maxSide) / float64(s)
	}
	nw, nh := int(float64(w)*f), int(float64(h)*f)
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	if f == 1 {
		draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Src)
		return dst
	}
	// Box filter: each new pixel averages the source pixels it covers.
	for y := 0; y < nh; y++ {
		y0, y1 := b.Min.Y+int(float64(y)/f), b.Min.Y+max(int(float64(y+1)/f), int(float64(y)/f)+1)
		for x := 0; x < nw; x++ {
			x0, x1 := b.Min.X+int(float64(x)/f), b.Min.X+max(int(float64(x+1)/f), int(float64(x)/f)+1)
			var r, g, bl, n uint32
			for sy := y0; sy < y1 && sy < b.Max.Y; sy++ {
				for sx := x0; sx < x1 && sx < b.Max.X; sx++ {
					cr, cg, cb, _ := src.At(sx, sy).RGBA()
					r, g, bl, n = r+cr, g+cg, bl+cb, n+1
				}
			}
			if n > 0 {
				dst.SetRGBA(x, y, color.RGBA{uint8(r / n >> 8), uint8(g / n >> 8), uint8(bl / n >> 8), 255})
			}
		}
	}
	return dst
}

// label is a printed word or phrase on a form that says what is written
// beside it.
type label struct {
	words []string
	field string // empty for a label whose value may be shown (age, sex)
}

var labels = []label{
	{[]string{"patient's", "name"}, "name"}, {[]string{"patients", "name"}, "name"}, {[]string{"patient", "name"}, "name"},
	{[]string{"name", "of", "patient"}, "name"}, {[]string{"full", "name"}, "name"}, {[]string{"first", "name"}, "first_name"},
	{[]string{"last", "name"}, "last_name"}, {[]string{"other", "names"}, "first_name"}, {[]string{"other", "name"}, "first_name"},
	{[]string{"surname"}, "last_name"}, {[]string{"family", "name"}, "last_name"}, {[]string{"given", "names"}, "first_name"}, {[]string{"name"}, "name"}, {[]string{"names"}, "name"}, {[]string{"patient"}, "name"},
	{[]string{"received", "from"}, "name"}, {[]string{"billed", "to"}, "name"}, {[]string{"bill", "to"}, "name"},
	{[]string{"next", "of", "kin"}, "name"}, {[]string{"client"}, "name"},
	{[]string{"membership"}, "member_number"}, {[]string{"member"}, "member_number"}, {[]string{"nhis"}, "member_number"},
	{[]string{"ghana", "card"}, "member_number"}, {[]string{"card", "no"}, "member_number"}, {[]string{"id", "no"}, "member_number"},
	{[]string{"folder"}, "member_number"}, {[]string{"opd", "no"}, "member_number"}, {[]string{"hospital", "no"}, "member_number"},
	{[]string{"policy"}, "policy_number"},
	{[]string{"date", "of", "birth"}, "birth_date"}, {[]string{"dob"}, "birth_date"}, {[]string{"birth"}, "birth_date"},
	{[]string{"telephone"}, "phone"}, {[]string{"tel"}, "phone"}, {[]string{"phone"}, "phone"}, {[]string{"mobile"}, "phone"},
	{[]string{"contact"}, "phone"},
	{[]string{"email"}, "email"}, {[]string{"e-mail"}, "email"},
	{[]string{"digital", "address"}, "address"}, {[]string{"address"}, "address"}, {[]string{"residence"}, "address"},
	{[]string{"house", "no"}, "address"}, {[]string{"location"}, "address"},
	// Labels whose values stay: they end a redacted place on the same line.
	{[]string{"age"}, ""}, {[]string{"sex"}, ""}, {[]string{"gender"}, ""}, {[]string{"date"}, ""}, {[]string{"diagnosis"}, ""},
	{[]string{"ward"}, ""}, {[]string{"occupation"}, ""}, {[]string{"signature"}, ""}, {[]string{"amount"}, ""},
}

// Words before "name" that make it someone else's: the clinic's, the
// doctor's, the insurer's.
var notPatient = map[string]bool{"provider": true, "facility": true, "doctor": true, "doctor's": true, "physician": true,
	"insurer": true, "scheme": true, "company": true, "hospital": true, "clinic": true, "prescriber": true, "prescriber's": true,
	"attending": true, "officer": true, "officer's": true, "pharmacist": true, "employer": true, "employer's": true, "drug": true}

var punct = regexp.MustCompile(`[^a-z0-9'-]`)

func normWord(s string) string {
	s = strings.ToLower(s)
	if strings.ReplaceAll(s, ".", "") == "dob" {
		return "dob"
	}
	return punct.ReplaceAllString(s, "")
}

type hit struct {
	field      string
	start, end int // word indexes on the line
}

// labelledRegions finds, on each line, the identifying labels and blacks out
// what follows each one up to the next label (or the page edge), with room
// above and below for handwriting. A label with nothing after it heads a
// column, so the space under it goes too.
func labelledRegions(words []Word, page image.Rectangle) ([]image.Rectangle, map[string]string, bool) {
	lines := map[string][]Word{}
	var order []string
	for _, w := range words {
		if _, ok := lines[w.Line]; !ok {
			order = append(order, w.Line)
		}
		lines[w.Line] = append(lines[w.Line], w)
	}
	var boxes []image.Rectangle
	found := map[string]string{}
	named := false
	type column struct {
		field string
		box   image.Rectangle
	}
	var columns []column
	for _, key := range order {
		ws := lines[key]
		sort.Slice(ws, func(i, j int) bool { return ws[i].Box.Min.X < ws[j].Box.Min.X })
		norm := make([]string, len(ws))
		for i, w := range ws {
			norm[i] = normWord(w.Text)
		}
		var hits []hit
		for i := 0; i < len(ws); i++ {
			for _, l := range labels {
				if i+len(l.words) > len(ws) || !slices.Equal(norm[i:i+len(l.words)], l.words) {
					continue
				}
				if isName(l.field) && i > 0 && notPatient[norm[i-1]] {
					break
				}
				// "No.", "Number", "ID" after a label are part of it.
				end := i + len(l.words)
				for end < len(ws) && (norm[end] == "no" || norm[end] == "number" || norm[end] == "id" || norm[end] == "#" || norm[end] == "") && !strings.ContainsAny(ws[end].Text, "0123456789") {
					end++
				}
				hits = append(hits, hit{l.field, i, end})
				i = end - 1
				break
			}
		}
		h := lineHeight(ws)
		for k, ht := range hits {
			if ht.field == "" {
				continue
			}
			named = named || isName(ht.field)
			x0 := ws[ht.end-1].Box.Max.X + 2
			x1, nextWord := page.Max.X, len(ws)
			if k+1 < len(hits) {
				x1, nextWord = ws[hits[k+1].start].Box.Min.X-2, hits[k+1].start
			}
			top, bot := ws[ht.start].Box.Min.Y, ws[ht.start].Box.Max.Y
			for _, w := range ws {
				top, bot = min(top, w.Box.Min.Y), max(bot, w.Box.Max.Y)
			}
			if x1 > x0 {
				boxes = append(boxes, image.Rect(x0, top-h*4/5, x1, bot+h/2))
			}
			var val []string
			for _, w := range ws[ht.end:nextWord] {
				val = append(val, w.Text)
			}
			if v := strings.Trim(strings.Join(val, " "), " :-#"); v != "" {
				keep(found, ht.field, v)
			} else {
				// Nothing beside it: a column heading, or a value written below.
				x0 := ws[ht.start].Box.Min.X - h
				x1 := max(x0+(ws[ht.end-1].Box.Max.X-ws[ht.start].Box.Min.X)*3, x0+h*12)
				if k+1 < len(hits) {
					x1 = ws[hits[k+1].start].Box.Min.X - 2
				}
				b := image.Rect(x0, bot, min(x1, page.Max.X), bot+h*4)
				boxes = append(boxes, b)
				columns = append(columns, column{ht.field, b})
			}
		}
	}
	// What is written under a heading is its value.
	for _, c := range columns {
		var in []Word
		for _, w := range words {
			if (image.Point{(w.Box.Min.X + w.Box.Max.X) / 2, (w.Box.Min.Y + w.Box.Max.Y) / 2}).In(c.box) {
				in = append(in, w)
			}
		}
		sort.Slice(in, func(i, j int) bool {
			if in[i].Line != in[j].Line {
				return in[i].Box.Min.Y < in[j].Box.Min.Y
			}
			return in[i].Box.Min.X < in[j].Box.Min.X
		})
		var val []string
		for _, w := range in {
			val = append(val, w.Text)
		}
		keep(found, c.field, strings.Trim(strings.Join(val, " "), " :-#"))
	}
	return boxes, found, named
}

func isName(field string) bool {
	return field == "name" || field == "first_name" || field == "last_name"
}

// keep records a value read for a field: the first one, except that names
// gather (surname, then other names).
func keep(found map[string]string, field, v string) {
	switch {
	case v == "":
	case found[field] == "":
		found[field] = v
	case isName(field) && !strings.Contains(found[field], v):
		found[field] += " " + v
	}
}

func lineHeight(ws []Word) int {
	hs := make([]int, len(ws))
	for i, w := range ws {
		hs[i] = w.Box.Dy()
	}
	slices.Sort(hs)
	return max(hs[len(hs)/2], 8)
}
