package sheet

import (
	"os"
	"reflect"
	"testing"
)

func TestReadXLSXFirstSheetInOrder(t *testing.T) {
	data, err := os.ReadFile("testdata/prices.xlsx")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := Read(data)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"Item Code", "Item Name", "Category", "Unit Price"},
		{"1001", "Paracetamol 500mg Tab", "Drugs", "0.5"},
		{"2002", "Full Blood Count", "Lab", "45"},
		{"C-3", "Consultation, GP", "", "1250.75"},
		{"GAP", "", "skipped a row and a column"},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("rows\n got %q\nwant %q", rows, want)
	}
}

func TestReadCSV(t *testing.T) {
	rows, err := Read([]byte("\xef\xbb\xbfcode;name;price\nA1;Malaria RDT;25,00\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rows, [][]string{{"code", "name", "price"}, {"A1", "Malaria RDT", "25,00"}}) {
		t.Fatalf("got %q", rows)
	}
	h := NewHeader([]string{" Item  Code ", "NAME"})
	if h.Col("code", "item code") != 0 || h.Col("name") != 1 || h.Col("price") != -1 {
		t.Fatalf("header %v", h)
	}
}

func TestMoney(t *testing.T) {
	for in, want := range map[string]int64{"1,250.50": 125050, "GHS 12": 1200, "0.5": 50, "₵3": 300, "45": 4500, "25,00": 2500, "1.250,50": 125050, "1,250": 125000} {
		if got, err := Money(in); err != nil || got != want {
			t.Errorf("Money(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"", "abc", "-3"} {
		if _, err := Money(in); err == nil {
			t.Errorf("Money(%q) should fail", in)
		}
	}
}
