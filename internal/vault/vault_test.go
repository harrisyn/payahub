package vault

import "testing"

func TestSealOpen(t *testing.T) {
	v, err := New(NewKey())
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := v.Seal(map[string]string{"password": "s3cret"}, "org_1/xceli/live")
	if err != nil {
		t.Fatal(err)
	}
	got, err := v.Open(sealed, "org_1/xceli/live")
	if err != nil || got["password"] != "s3cret" {
		t.Fatalf("open = %v, %v", got, err)
	}
	if _, err := v.Open(sealed, "org_2/xceli/live"); err == nil {
		t.Fatal("a sealed value opened under another owner")
	}
	other, _ := New(NewKey())
	if _, err := other.Open(sealed, "org_1/xceli/live"); err == nil {
		t.Fatal("a sealed value opened with another key")
	}
	if _, err := New("short"); err == nil {
		t.Fatal("accepted a bad key")
	}
}
