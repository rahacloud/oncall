package jalali

import "testing"

func TestRoundTrip(t *testing.T) {
	// Known anchor: 1 Farvardin 1400 == 21 March 2021.
	d := Date{1400, 1, 1}
	g := d.ToTime()
	if g.Year() != 2021 || g.Month() != 3 || g.Day() != 21 {
		t.Fatalf("1400/01/01 -> %v, want 2021-03-21", g.Format("2006-01-02"))
	}
	if back := FromTime(g); back != d {
		t.Fatalf("round trip: got %v want %v", back, d)
	}
}

func TestParse(t *testing.T) {
	for _, s := range []string{"1405/3/21", "1405-03-21"} {
		d, err := Parse(s)
		if err != nil {
			t.Fatalf("Parse(%q): %v", s, err)
		}
		if d != (Date{1405, 3, 21}) {
			t.Fatalf("Parse(%q) = %v", s, d)
		}
	}
	if _, err := Parse("nope"); err == nil {
		t.Fatal("expected error for bad date")
	}
}

func TestSpanLength(t *testing.T) {
	// 1405/3/21 .. 1405/4/20 inclusive should be 31 days.
	start := Date{1405, 3, 21}.ToTime()
	end := Date{1405, 4, 20}.ToTime()
	days := int(end.Sub(start).Hours()/24) + 1
	if days != 31 {
		t.Fatalf("span = %d days, want 31", days)
	}
}
