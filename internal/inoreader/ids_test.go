package inoreader

import "testing"

func TestIDConversion(t *testing.T) {
	cases := []struct{ long, short string }{
		{"tag:google.com,2005:reader/item/00000000148b9369", "344691561"},
		{"tag:google.com,2005:reader/item/00000000148b383e", "344668222"},
		{"tag:google.com,2005:reader/item/ffffffffffffffff", "-1"},
	}
	for _, c := range cases {
		s, err := ShortID(c.long)
		if err != nil || s != c.short {
			t.Errorf("ShortID(%q) = %q, %v; want %q", c.long, s, err, c.short)
		}
		s, err = ShortID(c.short)
		if err != nil || s != c.short {
			t.Errorf("ShortID(%q) = %q, %v; want unchanged", c.short, s, err)
		}
		l, err := LongID(c.short)
		if err != nil || l != c.long {
			t.Errorf("LongID(%q) = %q, %v; want %q", c.short, l, err, c.long)
		}
		l, err = LongID(c.long)
		if err != nil || l != c.long {
			t.Errorf("LongID(%q) = %q, %v; want unchanged", c.long, l, err)
		}
	}
	for _, bad := range []string{"", "abc", "tag:google.com,2005:reader/item/zz"} {
		if _, err := ShortID(bad); err == nil {
			t.Errorf("ShortID(%q) expected error", bad)
		}
		if _, err := LongID(bad); err == nil {
			t.Errorf("LongID(%q) expected error", bad)
		}
	}
}

func TestStreamHelpers(t *testing.T) {
	if got := LabelStream("Tech"); got != "user/-/label/Tech" {
		t.Errorf("LabelStream = %q", got)
	}
	if got := LabelStream("user/-/label/Tech"); got != "user/-/label/Tech" {
		t.Errorf("LabelStream passthrough = %q", got)
	}
	if got := FeedStream("https://x.example/rss"); got != "feed/https://x.example/rss" {
		t.Errorf("FeedStream = %q", got)
	}
	if got := FeedStream("feed/https://x.example/rss"); got != "feed/https://x.example/rss" {
		t.Errorf("FeedStream passthrough = %q", got)
	}
	if LabelName("user/123/label/Tech") != "Tech" || LabelName("user/-/state/com.google/read") != "" {
		t.Error("LabelName")
	}
	if !IsSystemState("user/-/state/com.google/starred") || IsSystemState("user/-/label/x") {
		t.Error("IsSystemState")
	}
	if got := escapeStreamID("user/-/state/com.google/reading-list"); got != "user%2F-%2Fstate%2Fcom.google%2Freading-list" {
		t.Errorf("escapeStreamID = %q", got)
	}
}

func TestStripHTML(t *testing.T) {
	in := `<p>Hello&nbsp;<b>world</b></p><script>var x = "<evil>";</script><style>p{}</style>
	<div>  Second   line &amp; more</div>`
	want := "Hello world Second line & more"
	if got := StripHTML(in); got != want {
		t.Errorf("StripHTML = %q, want %q", got, want)
	}
	if got := Truncate("a long sentence with many words", 12); got != "a long…" {
		t.Errorf("Truncate = %q", got)
	}
	if got := Truncate("short", 12); got != "short" {
		t.Errorf("Truncate short = %q", got)
	}
}
