package claim

import (
	"fmt"
	"time"
)

// Date is a calendar date with no time zone, serialised as "YYYY-MM-DD".
type Date struct{ time.Time }

const dateLayout = "2006-01-02"

func NewDate(y int, m time.Month, d int) Date { return Date{time.Date(y, m, d, 0, 0, 0, 0, time.UTC)} }

func (d Date) MarshalJSON() ([]byte, error) {
	if d.IsZero() {
		return []byte("null"), nil
	}
	return []byte(`"` + d.Format(dateLayout) + `"`), nil
}

func (d *Date) UnmarshalJSON(b []byte) error {
	s := string(b)
	if s == "null" || s == `""` {
		d.Time = time.Time{}
		return nil
	}
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return fmt.Errorf("date must be a string like \"2026-10-20\"")
	}
	t, err := time.Parse(dateLayout, s[1:len(s)-1])
	if err != nil {
		return fmt.Errorf("date must look like \"2026-10-20\"")
	}
	d.Time = t
	return nil
}
