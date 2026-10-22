// Package sheet reads the first table of a CSV or Excel (.xlsx) file into
// rows of text, so price lists and code lists can come from either. It uses
// the standard library only: an .xlsx file is a zip of XML parts.
package sheet

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"
)

// MaxRows bounds a file so one upload cannot exhaust memory.
const MaxRows = 200_000

// Read returns the rows of a CSV file, or of the first worksheet of an .xlsx
// file, which it recognises by content rather than by name.
func Read(data []byte) ([][]string, error) {
	if bytes.HasPrefix(data, []byte("PK\x03\x04")) {
		return readXLSX(data)
	}
	return readCSV(data)
}

func readCSV(data []byte) ([][]string, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	rd := csv.NewReader(bytes.NewReader(data))
	rd.FieldsPerRecord = -1
	rd.TrimLeadingSpace = true
	rd.LazyQuotes = true
	// A semicolon-separated export (common where the comma is the decimal
	// mark) is told apart by its header line.
	if first, _, _ := bytes.Cut(data, []byte("\n")); bytes.Count(first, []byte(";")) > bytes.Count(first, []byte(",")) {
		rd.Comma = ';'
	}
	var rows [][]string
	for {
		rec, err := rd.Read()
		if errors.Is(err, io.EOF) {
			return rows, nil
		}
		if err != nil {
			return nil, err
		}
		if len(rows) == MaxRows {
			return nil, fmt.Errorf("the file has more than %d rows", MaxRows)
		}
		rows = append(rows, rec)
	}
}

type xlsxCell struct {
	Ref    string `xml:"r,attr"`
	Type   string `xml:"t,attr"`
	Value  string `xml:"v"`
	Inline struct {
		Text string     `xml:"t"`
		Runs []xlsxText `xml:"r"`
	} `xml:"is"`
}

type xlsxText struct {
	Text string `xml:"t"`
}

func readXLSX(data []byte) ([][]string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("not a readable Excel file: %w", err)
	}
	files := map[string]*zip.File{}
	for _, f := range zr.File {
		files[f.Name] = f
	}
	open := func(name string) (io.ReadCloser, error) {
		f, ok := files[name]
		if !ok {
			return nil, fmt.Errorf("the Excel file has no %s", name)
		}
		if f.UncompressedSize64 > 256<<20 {
			return nil, errors.New("the Excel file is too large")
		}
		return f.Open()
	}
	shared, err := sharedStrings(files, open)
	if err != nil {
		return nil, err
	}
	name, err := firstSheet(open)
	if err != nil {
		return nil, err
	}
	rc, err := open(name)
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	var rows [][]string
	dec := xml.NewDecoder(rc)
	var row []string
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading the worksheet: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "row":
				row = nil
			case "c":
				var c xlsxCell
				if err := dec.DecodeElement(&c, &t); err != nil {
					return nil, err
				}
				col := column(c.Ref)
				if col < 0 {
					col = len(row)
				}
				if col > 16_384 {
					return nil, errors.New("the worksheet has a cell beyond the last column")
				}
				for len(row) < col {
					row = append(row, "")
				}
				row = append(row[:col], cellText(c, shared))
			}
		case xml.EndElement:
			if t.Name.Local == "row" {
				if len(rows) == MaxRows {
					return nil, fmt.Errorf("the file has more than %d rows", MaxRows)
				}
				rows = append(rows, row)
			}
		}
	}
	return rows, nil
}

func cellText(c xlsxCell, shared []string) string {
	switch c.Type {
	case "s":
		i, err := strconv.Atoi(c.Value)
		if err != nil || i < 0 || i >= len(shared) {
			return ""
		}
		return shared[i]
	case "inlineStr":
		if c.Inline.Text != "" {
			return c.Inline.Text
		}
		var b strings.Builder
		for _, r := range c.Inline.Runs {
			b.WriteString(r.Text)
		}
		return b.String()
	case "b":
		if c.Value == "1" {
			return "TRUE"
		}
		return "FALSE"
	default:
		return c.Value
	}
}

// column turns a cell reference such as "AB12" into a 0-based column.
func column(ref string) int {
	n := 0
	i := 0
	for ; i < len(ref) && ref[i] >= 'A' && ref[i] <= 'Z'; i++ {
		n = n*26 + int(ref[i]-'A'+1)
	}
	if i == 0 {
		return -1
	}
	return n - 1
}

func sharedStrings(files map[string]*zip.File, open func(string) (io.ReadCloser, error)) ([]string, error) {
	if _, ok := files["xl/sharedStrings.xml"]; !ok {
		return nil, nil
	}
	rc, err := open("xl/sharedStrings.xml")
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	var sst struct {
		Items []struct {
			Text string     `xml:"t"`
			Runs []xlsxText `xml:"r"`
		} `xml:"si"`
	}
	if err := xml.NewDecoder(rc).Decode(&sst); err != nil {
		return nil, fmt.Errorf("reading the Excel strings: %w", err)
	}
	out := make([]string, len(sst.Items))
	for i, si := range sst.Items {
		if len(si.Runs) == 0 {
			out[i] = si.Text
			continue
		}
		var b strings.Builder
		for _, r := range si.Runs {
			b.WriteString(r.Text)
		}
		out[i] = b.String()
	}
	return out, nil
}

// firstSheet finds the first worksheet in workbook order through the
// workbook's relationships.
func firstSheet(open func(string) (io.ReadCloser, error)) (string, error) {
	rc, err := open("xl/workbook.xml")
	if err != nil {
		return "", err
	}
	var wb struct {
		Sheets []struct {
			ID string `xml:"http://schemas.openxmlformats.org/officeDocument/2006/relationships id,attr"`
		} `xml:"sheets>sheet"`
	}
	err = xml.NewDecoder(rc).Decode(&wb)
	rc.Close()
	if err != nil || len(wb.Sheets) == 0 {
		return "", errors.New("the Excel file has no worksheet")
	}
	rc, err = open("xl/_rels/workbook.xml.rels")
	if err != nil {
		return "", err
	}
	defer rc.Close()
	var rels struct {
		Rels []struct {
			ID     string `xml:"Id,attr"`
			Target string `xml:"Target,attr"`
		} `xml:"Relationship"`
	}
	if err := xml.NewDecoder(rc).Decode(&rels); err != nil {
		return "", err
	}
	for _, r := range rels.Rels {
		if r.ID == wb.Sheets[0].ID {
			if strings.HasPrefix(r.Target, "/") {
				return strings.TrimPrefix(r.Target, "/"), nil
			}
			return path.Join("xl", r.Target), nil
		}
	}
	return "", errors.New("the Excel file's first worksheet is missing")
}

// Header finds columns by their header names, case and spacing ignored.
type Header map[string]int

func NewHeader(row []string) Header {
	h := Header{}
	for i, name := range row {
		key := strings.Join(strings.Fields(strings.ToLower(name)), " ")
		if _, dup := h[key]; !dup && key != "" {
			h[key] = i
		}
	}
	return h
}

// Col is the first of names present, or -1.
func (h Header) Col(names ...string) int {
	for _, n := range names {
		if i, ok := h[n]; ok {
			return i
		}
	}
	return -1
}

// Cell is row[i] trimmed, or "" when the row is short or i is -1.
func Cell(row []string, i int) string {
	if i < 0 || i >= len(row) {
		return ""
	}
	return strings.TrimSpace(row[i])
}

// Money parses an amount in major units ("1,250.50", "GHS 12") into minor
// units.
func Money(s string) (int64, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimLeft(s, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz₵$ ")
	// "25,00" or "1.250,50": a comma followed by exactly two digits at the
	// end, with no dot after it, is a decimal comma.
	if i := strings.LastIndexByte(s, ','); i >= 0 && len(s)-i == 3 && !strings.Contains(s[i:], ".") {
		s = strings.ReplaceAll(s[:i], ".", "") + "." + s[i+1:]
	}
	s = strings.ReplaceAll(s, ",", "")
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f < 0 || f > 1e12 {
		return 0, fmt.Errorf("%q is not an amount", s)
	}
	return int64(f*100 + 0.5), nil
}
