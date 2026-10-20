package main

import "testing"

func TestCleanBlazingothText(t *testing.T) {
	cases := map[string]string{
		"women jiggling when they walk is just so beautiful ?????":       "women jiggling when they walk is just so beautiful",
		"i heavily fuck with lovey dovey shit  / thank u bye":            "i heavily fuck with lovey dovey shit\nthank u bye",
		"if i'm greeted with the button.  /  / you. like. fat. bitches.": "if i'm greeted with the button.\n\nyou. like. fat. bitches.",
		"should i post more tummy?":                                      "should i post more tummy?",
		"dd/lg brat":                                                     "dd/lg brat",
	}
	for in, want := range cases {
		if got := cleanBlazingothText(in); got != want {
			t.Errorf("cleanBlazingothText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNextBlazingothPostSkipsUsed(t *testing.T) {
	posts := []string{"one", "", "two", "three"}
	c := blazingothCursor{Posted: map[string]int{"two": 2}}
	i, text, ok := nextBlazingothPost(posts, c)
	if !ok || i != 0 || text != "one" {
		t.Fatalf("got %d %q %v", i, text, ok)
	}
	c.Next = 1
	i, text, ok = nextBlazingothPost(posts, c)
	if !ok || i != 3 || text != "three" {
		t.Fatalf("got %d %q %v", i, text, ok)
	}
}
