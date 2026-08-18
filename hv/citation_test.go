package main

import "testing"

// Wire these to your implementation.
// Citation should return the string (not print it), and take a flag or be a
// separate func that appends [i] indices after each </yellow> block.
//
//   func Citation(sentence string, phrases []string) string
//   func CitationWithCites(sentence string, phrases []string) string

type tc struct {
	name     string
	sentence string
	phrases  []string
	want     string // basic tagging, no citation indices
	wantCite string // with [i] indices appended after each block
}

var cases = []tc{
	// ---------- sanity ----------
	{
		name:     "basic single match",
		sentence: "The quick brown fox", phrases: []string{"quick brown"},
		want:     "The <yellow>quick brown</yellow> fox",
		wantCite: "The <yellow>quick brown</yellow>[0] fox",
	},
	{
		name:     "match at very start",
		sentence: "alpha beta gamma", phrases: []string{"alpha"},
		want:     "<yellow>alpha</yellow> beta gamma",
		wantCite: "<yellow>alpha</yellow>[0] beta gamma",
	},
	{
		name:     "match at very end",
		sentence: "alpha beta gamma", phrases: []string{"gamma"},
		want:     "alpha beta <yellow>gamma</yellow>",
		wantCite: "alpha beta <yellow>gamma</yellow>[0]",
	},
	{
		name:     "entire sentence matches",
		sentence: "alpha beta gamma", phrases: []string{"alpha beta gamma"},
		want:     "<yellow>alpha beta gamma</yellow>",
		wantCite: "<yellow>alpha beta gamma</yellow>[0]",
	},
	{
		name:     "single word sentence",
		sentence: "hello", phrases: []string{"hello"},
		want:     "<yellow>hello</yellow>",
		wantCite: "<yellow>hello</yellow>[0]",
	},

	// ---------- empty / degenerate: these panic on unguarded code ----------
	{
		name:     "no matches found",
		sentence: "hello world", phrases: []string{"nothing here"},
		want: "hello world", wantCite: "hello world",
	},
	{
		name:     "empty phrase list",
		sentence: "hello world", phrases: []string{},
		want: "hello world", wantCite: "hello world",
	},
	{
		name:     "nil phrase list",
		sentence: "hello world", phrases: nil,
		want: "hello world", wantCite: "hello world",
	},
	{
		name:     "empty sentence",
		sentence: "", phrases: []string{"foo"},
		want: "", wantCite: "",
	},
	{
		name:     "whitespace-only sentence",
		sentence: "   ", phrases: []string{"foo"},
		want: "   ", wantCite: "   ",
	},
	{
		name:     "phrase longer than sentence",
		sentence: "short", phrases: []string{"short and long"},
		want: "short", wantCite: "short",
	},

	// ---------- word boundary ----------
	{
		name:     "substring must not match (blueprint/blue)",
		sentence: "we saw a blueprint today", phrases: []string{"blue"},
		want: "we saw a blueprint today", wantCite: "we saw a blueprint today",
	},
	{
		name:     "substring must not match (brown/row)",
		sentence: "quick brown fox jumps", phrases: []string{"row"},
		want: "quick brown fox jumps", wantCite: "quick brown fox jumps",
	},
	{
		name:     "case sensitivity",
		sentence: "The the THE", phrases: []string{"the"},
		want:     "The <yellow>the</yellow> THE",
		wantCite: "The <yellow>the</yellow>[0] THE",
	},

	// ---------- the merge rules ----------
	{
		name:     "nested span is absorbed",
		sentence: "The quick brown fox jumps", phrases: []string{"quick brown fox", "fox"},
		want:     "The <yellow>quick brown fox</yellow> jumps",
		wantCite: "The <yellow>quick brown fox</yellow>[0][1] jumps",
	},
	{
		name:     "overlap longer than one word",
		sentence: "who lets the dog out", phrases: []string{"who lets the dog", "the dog out"},
		want:     "<yellow>who lets the dog out</yellow>",
		wantCite: "<yellow>who lets the dog out</yellow>[0][1]",
	},
	{
		name:     "ADJACENT but not overlapping stays separate",
		sentence: "foo bar abc xyz abc xyz foo bar", phrases: []string{"abc xyz abc", "xyz"},
		want:     "foo bar <yellow>abc xyz abc</yellow> <yellow>xyz</yellow> foo bar",
		wantCite: "foo bar <yellow>abc xyz abc</yellow>[1][0] <yellow>xyz</yellow>[1] foo bar",
	},
	{
		name:     "gap separated stays separate",
		sentence: "a b c d e", phrases: []string{"a b", "d e"},
		want:     "<yellow>a b</yellow> c <yellow>d e</yellow>",
		wantCite: "<yellow>a b</yellow>[0] c <yellow>d e</yellow>[1]",
	},
	{
		name:     "chain merge A-B B-C",
		sentence: "one two three four five", phrases: []string{"one two", "two three", "three four"},
		want:     "<yellow>one two three four</yellow> five",
		wantCite: "<yellow>one two three four</yellow>[0][1][2] five",
	},
	{
		name:     "same phrase multiple occurrences",
		sentence: "cat dog cat dog cat", phrases: []string{"cat"},
		want:     "<yellow>cat</yellow> dog <yellow>cat</yellow> dog <yellow>cat</yellow>",
		wantCite: "<yellow>cat</yellow>[0] dog <yellow>cat</yellow>[0] dog <yellow>cat</yellow>[0]",
	},
	{
		name:     "overlapping occurrences of the SAME phrase",
		sentence: "a b a b a", phrases: []string{"a b a"},
		want:     "<yellow>a b a b a</yellow>",
		wantCite: "<yellow>a b a b a</yellow>[0]",
	},
	{
		name:     "duplicate phrase in list",
		sentence: "hello world", phrases: []string{"world", "world"},
		want:     "hello <yellow>world</yellow>",
		wantCite: "hello <yellow>world</yellow>[0][1]",
	},
	{
		name:     "every word matched separately",
		sentence: "x y z", phrases: []string{"x", "y", "z"},
		want:     "<yellow>x</yellow> <yellow>y</yellow> <yellow>z</yellow>",
		wantCite: "<yellow>x</yellow>[0] <yellow>y</yellow>[1] <yellow>z</yellow>[2]",
	},

	// ---------- whitespace fidelity: catches the tokenizer bug ----------
	{
		name:     "double spaces preserved",
		sentence: "foo  bar   baz", phrases: []string{"bar"},
		want:     "foo  <yellow>bar</yellow>   baz",
		wantCite: "foo  <yellow>bar</yellow>[0]   baz",
	},
	{
		name:     "leading and trailing spaces preserved",
		sentence: "  foo bar  ", phrases: []string{"foo bar"},
		want:     "  <yellow>foo bar</yellow>  ",
		wantCite: "  <yellow>foo bar</yellow>[0]  ",
	},
	{
		name:     "newline inside a matched span",
		sentence: "foo\nbar baz", phrases: []string{"foo bar"},
		want:     "<yellow>foo\nbar</yellow> baz",
		wantCite: "<yellow>foo\nbar</yellow>[0] baz",
	},
	{
		name:     "tab separated",
		sentence: "foo\tbar", phrases: []string{"foo bar"},
		want:     "<yellow>foo\tbar</yellow>",
		wantCite: "<yellow>foo\tbar</yellow>[0]",
	},

	// ---------- punctuation: clarify the rule with your interviewer ----------
	{
		name:     "punctuation attached blocks the match (whitespace tokenizing)",
		sentence: "the lazy dog.", phrases: []string{"lazy dog"},
		want: "the lazy dog.", wantCite: "the lazy dog.",
	},
	{
		name:     "punctuation as its own token",
		sentence: "the lazy dog .", phrases: []string{"lazy dog"},
		want:     "the <yellow>lazy dog</yellow> .",
		wantCite: "the <yellow>lazy dog</yellow>[0] .",
	},

	// ---------- citation ordering ----------
	{
		name:     "freq tie falls back to index ascending",
		sentence: "quick brown fox", phrases: []string{"quick brown fox", "fox"},
		want:     "<yellow>quick brown fox</yellow>",
		wantCite: "<yellow>quick brown fox</yellow>[0][1]",
	},
	{
		name:     "absorbed match still counts toward frequency",
		sentence: "foo bar abc xyz abc xyz foo bar", phrases: []string{"abc xyz abc", "xyz"},
		want:     "foo bar <yellow>abc xyz abc</yellow> <yellow>xyz</yellow> foo bar",
		wantCite: "foo bar <yellow>abc xyz abc</yellow>[1][0] <yellow>xyz</yellow>[1] foo bar",
	},
	{
		name:     "frequency 3 vs 1",
		sentence: "a b a b a b c", phrases: []string{"a b", "c"},
		want:     "<yellow>a b</yellow> <yellow>a b</yellow> <yellow>a b</yellow> <yellow>c</yellow>",
		wantCite: "<yellow>a b</yellow>[0] <yellow>a b</yellow>[0] <yellow>a b</yellow>[0] <yellow>c</yellow>[1]",
	},
}

func TestCitation(t *testing.T) {
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := citation(c.sentence, c.phrases)
			if got != c.want {
				t.Errorf("\nsentence: %q\nphrases : %q\nwant    : %q\ngot     : %q",
					c.sentence, c.phrases, c.want, got)
			}
		})
	}
}

func TestCitationWithIndices(t *testing.T) {
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := citation(c.sentence, c.phrases)
			if got != c.wantCite {
				t.Errorf("\nsentence: %q\nphrases : %q\nwant    : %q\ngot     : %q",
					c.sentence, c.phrases, c.wantCite, got)
			}
		})
	}
}

// Determinism guard: Go randomizes map iteration order. If citation indices are
// emitted straight from a map without sorting, this catches it.
func TestDeterministic(t *testing.T) {
	for _, c := range cases {
		first := citation(c.sentence, c.phrases)
		for i := 0; i < 200; i++ {
			if got := citation(c.sentence, c.phrases); got != first {
				t.Fatalf("%s: nondeterministic output\n run 0: %q\n run %d: %q",
					c.name, first, i, got)
			}
		}
	}
}
