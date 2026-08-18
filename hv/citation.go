package main

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
)

type Token struct {
	word  string
	start int
	end   int
}

type Interval struct {
	start       int
	end         int
	matchedWith []int
}

func citation(sentence string, phrases []string) string {
	sTokens := tokenise(sentence)
	intervals := make([]Interval, 0)
	fmt.Println(sTokens)
	for i := 0; i < len(sTokens); i++ {
		for j, phrase := range phrases {
			pTokens := strings.Split(phrase, " ")
			if matchesAt(sTokens, pTokens, i) {
				intervals = append(intervals, Interval{i, i + len(pTokens) - 1, []int{j}})
			}
		}
	}
	mInterval := mergeIntervals(intervals)

	var b strings.Builder
	prev := 0
	for _, interval := range mInterval {
		s, e := sTokens[interval.start].start, sTokens[interval.end].end
		b.WriteString(sentence[prev:s])
		b.WriteString("<yellow>")
		b.WriteString(sentence[s : e+1])
		b.WriteString("</yellow>")
		prev = e + 1
	}
	b.WriteString(sentence[prev:])
	return b.String()
}

func mergeIntervals(intervals []Interval) []Interval {
	if len(intervals) == 0 {
		return nil
	}
	sort.Slice(intervals, func(i, j int) bool {
		return intervals[i].start <= intervals[j].start
	})
	intervalToMerge := intervals[0]
	mergedIntervals := make([]Interval, 0)
	for i := 1; i < len(intervals); i++ {
		if intervalToMerge.end >= intervals[i].start {
			intervalToMerge.start = min(intervalToMerge.start, intervals[i].start)
			intervalToMerge.end = max(intervalToMerge.end, intervals[i].end)
			intervalToMerge.matchedWith = append(intervalToMerge.matchedWith, i)
		} else {
			mergedIntervals = append(mergedIntervals, intervalToMerge)
			intervalToMerge = intervals[i]
		}
	}
	mergedIntervals = append(mergedIntervals, intervalToMerge)
	return mergedIntervals
}

func matchesAt(sTokens []Token, pTokens []string, i int) bool {
	if i+len(pTokens) > len(sTokens) {
		return false
	}
	for j := 0; j < len(pTokens); j++ {
		if sTokens[i+j].word != pTokens[j] {
			return false
		}
	}
	return true
}

func tokenise(sentence string) []Token {
	tokens := make([]Token, 0)
	for i := 0; i < len(sentence); i++ {
		if unicode.IsSpace(rune(sentence[i])) {
			continue
		}
		var j int
		s := i
		for j = i; j < len(sentence) && !unicode.IsSpace(rune(sentence[j])); j++ {
		}
		word := sentence[s:j]
		i = j
		tokens = append(tokens, Token{word, s, j - 1})
	}
	return tokens
}
