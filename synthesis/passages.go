package synthesis

import (
	"sort"
	"strings"
	"unicode"
)

// Limits for turning artifact chunks into evidence. Real adapters return one
// chunk per object (a whole document or transcript), so each chunk is split
// into passages and only the passages that best match the query are kept.
const (
	maxPassageRunes     = 600
	minPassageRunes     = 80
	maxPassagesPerChunk = 4
	// maxChunkEvidenceBytes caps the passage text of one evidence pack, well
	// below the 4 MiB request limit of the AI answerer.
	maxChunkEvidenceBytes = 512 << 10
)

// passage is a rune range [start, end) of a chunk's text.
type passage struct {
	start, end int
	heading    bool
}

// splitPassages splits text into passages at blank lines and Markdown
// headings, splits passages longer than maxPassageRunes at line breaks, then
// sentence ends, then hard limits, and merges passages shorter than
// minPassageRunes with the following one inside the same section.
func splitPassages(r []rune) []passage {
	var blocks []passage
	blockStart := -1
	closeBlock := func(end int) {
		if blockStart >= 0 && end > blockStart {
			blocks = append(blocks, passage{start: blockStart, end: end, heading: isHeading(r[blockStart:end])})
		}
		blockStart = -1
	}
	for lineStart := 0; lineStart < len(r); {
		lineEnd := lineStart
		for lineEnd < len(r) && r[lineEnd] != '\n' {
			lineEnd++
		}
		line := r[lineStart:lineEnd]
		switch {
		case strings.TrimSpace(string(line)) == "":
			closeBlock(lineStart)
		case isHeading(line):
			closeBlock(lineStart)
			blockStart = lineStart
		case blockStart < 0:
			blockStart = lineStart
		}
		lineStart = lineEnd + 1
	}
	closeBlock(len(r))

	var pieces []passage
	for _, block := range blocks {
		pieces = append(pieces, splitLong(r, block)...)
	}
	var merged []passage
	for _, piece := range pieces {
		if n := len(merged); n > 0 {
			last := &merged[n-1]
			if last.end-last.start < minPassageRunes && !piece.heading && piece.end-last.start <= maxPassageRunes {
				last.end = piece.end
				continue
			}
		}
		merged = append(merged, piece)
	}
	out := merged[:0]
	for _, p := range merged {
		if p = trimPassage(r, p); p.end > p.start {
			out = append(out, p)
		}
	}
	return out
}

func isHeading(line []rune) bool {
	return strings.HasPrefix(strings.TrimSpace(string(line)), "#")
}

func splitLong(r []rune, p passage) []passage {
	if p.end-p.start <= maxPassageRunes {
		return []passage{p}
	}
	for _, boundary := range []func(rune) bool{
		func(c rune) bool { return c == '\n' },
		func(c rune) bool { return strings.ContainsRune("。！？!?；;", c) },
	} {
		if parts := splitAt(r, p, boundary); len(parts) > 1 {
			var out []passage
			for _, part := range parts {
				out = append(out, splitLong(r, part)...)
			}
			out[0].heading = p.heading
			return out
		}
	}
	var out []passage
	for start := p.start; start < p.end; start += maxPassageRunes {
		end := start + maxPassageRunes
		if end > p.end {
			end = p.end
		}
		out = append(out, passage{start: start, end: end})
	}
	out[0].heading = p.heading
	return out
}

// splitAt packs the segments that end at boundary runes greedily into parts of
// at most maxPassageRunes. A single segment longer than that becomes its own
// part and is split again by the caller.
func splitAt(r []rune, p passage, boundary func(rune) bool) []passage {
	var segments []passage
	start := p.start
	for i := p.start; i < p.end; i++ {
		if boundary(r[i]) {
			segments = append(segments, passage{start: start, end: i + 1})
			start = i + 1
		}
	}
	if start < p.end {
		segments = append(segments, passage{start: start, end: p.end})
	}
	if len(segments) < 2 {
		return nil
	}
	var parts []passage
	current := segments[0]
	for _, segment := range segments[1:] {
		if segment.end-current.start <= maxPassageRunes {
			current.end = segment.end
			continue
		}
		parts = append(parts, current)
		current = segment
	}
	return append(parts, current)
}

func trimPassage(r []rune, p passage) passage {
	for p.start < p.end && unicode.IsSpace(r[p.start]) {
		p.start++
	}
	for p.end > p.start && unicode.IsSpace(r[p.end-1]) {
		p.end--
	}
	return p
}

// queryTerms returns the distinct lower-case terms of query: words of other
// scripts as written, and Han text as overlapping two-character terms (a
// single Han character when it stands alone).
func queryTerms(query string) []string {
	seen := map[string]bool{}
	var terms []string
	add := func(term string) {
		if term != "" && !seen[term] {
			seen[term] = true
			terms = append(terms, term)
		}
	}
	for _, word := range strings.FieldsFunc(strings.ToLower(query), func(c rune) bool {
		return unicode.IsSpace(c) || unicode.IsPunct(c) || unicode.IsSymbol(c)
	}) {
		var han, other []rune
		flush := func() {
			if len(han) == 1 {
				add(string(han))
			}
			for i := 0; i+1 < len(han); i++ {
				add(string(han[i : i+2]))
			}
			if len(other) > 1 {
				add(string(other))
			}
			han, other = han[:0], other[:0]
		}
		for _, c := range word {
			if unicode.Is(unicode.Han, c) {
				if len(other) > 0 {
					flush()
				}
				han = append(han, c)
			} else {
				if len(han) > 0 {
					flush()
				}
				other = append(other, c)
			}
		}
		flush()
	}
	return terms
}

// selectPassages keeps at most maxPassagesPerChunk passages: those matching
// the most query terms, earlier ones first on ties, returned in text order.
func selectPassages(r []rune, passages []passage, terms []string) []passage {
	if len(passages) <= maxPassagesPerChunk {
		return passages
	}
	type scored struct {
		index, score int
	}
	ranked := make([]scored, len(passages))
	for i, p := range passages {
		text := strings.ToLower(string(r[p.start:p.end]))
		score := 0
		for _, term := range terms {
			if strings.Contains(text, term) {
				score++
			}
		}
		ranked[i] = scored{index: i, score: score}
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].score > ranked[j].score })
	keep := make([]int, 0, maxPassagesPerChunk)
	for _, s := range ranked[:maxPassagesPerChunk] {
		keep = append(keep, s.index)
	}
	sort.Ints(keep)
	out := make([]passage, 0, len(keep))
	for _, i := range keep {
		out = append(out, passages[i])
	}
	return out
}
