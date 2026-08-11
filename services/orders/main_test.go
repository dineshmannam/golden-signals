package main

import "testing"

func TestParseID(t *testing.T) {
	cases := []struct {
		path   string
		wantID int64
		wantOK bool
	}{
		{"/orders", 0, false},
		{"/orders/", 0, false},
		{"/orders/42", 42, true},
		{"/orders/abc", 0, false},
		{"/orders/7/", 7, true},
	}
	for _, c := range cases {
		id, ok := parseID(c.path)
		if id != c.wantID || ok != c.wantOK {
			t.Errorf("parseID(%q) = (%d,%v), want (%d,%v)", c.path, id, ok, c.wantID, c.wantOK)
		}
	}
}
