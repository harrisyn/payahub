// Command gen rebuilds data/icd10cm.tsv.gz from a CMS ICD-10-CM release: the
// tabular list (XML) and, to keep only valid codes, the release's list of
// every code, one per line.
//
//	go run ./internal/icd10/gen -tabular icd10cm-tabular-2026.xml -codes codes.txt -out internal/icd10/data/icd10cm.tsv.gz
//
// Each row is the code without its dot, 1 when it is billable at that level,
// and its title. Codes that take a seventh character are written out with
// each one ("S00.01XA", "..., initial encounter").
package main

import (
	"bufio"
	"compress/gzip"
	"encoding/xml"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
)

type extension struct {
	Char string `xml:"char,attr"`
	Text string `xml:",chardata"`
}

type diag struct {
	Name  string       `xml:"name"`
	Desc  string       `xml:"desc"`
	Seven *[]extension `xml:"sevenChrDef>extension"`
	Diags []diag       `xml:"diag"`
}

type tabular struct {
	Version  string `xml:"version"`
	Chapters []struct {
		Sections []struct {
			Diags []diag `xml:"diag"`
		} `xml:"section"`
	} `xml:"chapter"`
}

func main() {
	tab := flag.String("tabular", "", "the release's tabular list (XML)")
	codes := flag.String("codes", "", "the release's list of valid codes, one per line")
	out := flag.String("out", "internal/icd10/data/icd10cm.tsv.gz", "where to write")
	flag.Parse()

	valid := map[string]bool{}
	f, err := os.Open(*codes)
	if err != nil {
		log.Fatal(err)
	}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if c := strings.ReplaceAll(strings.TrimSpace(sc.Text()), ".", ""); c != "" {
			valid[c] = true
		}
	}
	f.Close()

	raw, err := os.ReadFile(*tab)
	if err != nil {
		log.Fatal(err)
	}
	var t tabular
	if err := xml.Unmarshal(raw, &t); err != nil {
		log.Fatal(err)
	}
	var rows []string
	billable := 0
	add := func(code, b, title string) {
		if !valid[code] {
			return
		}
		rows = append(rows, code+"\t"+b+"\t"+title)
		if b == "1" {
			billable++
		}
	}
	var walk func(d diag, inherited []extension)
	walk = func(d diag, inherited []extension) {
		ext := inherited
		if d.Seven != nil {
			ext = *d.Seven
		}
		code := strings.ReplaceAll(strings.TrimSpace(d.Name), ".", "")
		desc := strings.Join(strings.Fields(d.Desc), " ")
		leaf := len(d.Diags) == 0
		b := "0"
		if leaf && len(ext) == 0 {
			b = "1"
		}
		add(code, b, desc)
		for _, k := range d.Diags {
			walk(k, ext)
		}
		if leaf {
			base := code + strings.Repeat("X", max(0, 6-len(code)))
			for _, e := range ext {
				add(base+e.Char, "1", desc+", "+strings.TrimSpace(e.Text))
			}
		}
	}
	for _, c := range t.Chapters {
		for _, s := range c.Sections {
			for _, d := range s.Diags {
				walk(d, nil)
			}
		}
	}
	w, err := os.Create(*out)
	if err != nil {
		log.Fatal(err)
	}
	gz, _ := gzip.NewWriterLevel(w, gzip.BestCompression)
	for _, r := range rows {
		fmt.Fprintln(gz, r)
	}
	if err := gz.Close(); err != nil {
		log.Fatal(err)
	}
	if err := w.Close(); err != nil {
		log.Fatal(err)
	}
	log.Printf("ICD-10-CM %s: %d codes, %d billable", t.Version, len(rows), billable)
}
