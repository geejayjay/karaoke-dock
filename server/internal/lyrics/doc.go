// Package lyrics fetches, parses and shapes lyrics.
package lyrics

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Doc is the stored lyrics.json format.
type Doc struct {
	Version  int       `json:"version"`
	Source   string    `json:"source"`
	Timing   string    `json:"timing"` // "line" or "word"
	Language string    `json:"language,omitempty"`
	OffsetMs int64     `json:"offsetMs"`
	Voices   []Voice   `json:"voices"`
	Lines    []Line    `json:"lines"`
	Original *Original `json:"original,omitempty"`
}

// Original keeps LRCLIB's text, so lyrics can be fixed with no new lookup.
type Original struct {
	Plain  string `json:"plain,omitempty"`
	Synced string `json:"synced,omitempty"`
}

// Voice is one singing part.
type Voice struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Source string `json:"source"`
}

// Line is one lyric line.
type Line struct {
	StartMs int64  `json:"startMs"`
	EndMs   int64  `json:"endMs"`
	Voice   string `json:"voice,omitempty"`
	Words   []Word `json:"words"`
	// Backing holds backing vocals sung with this line, on their own timing.
	Backing []Word `json:"backing,omitempty"`
	// BackingOnly marks a line that's entirely backing vocals.
	BackingOnly bool `json:"backingOnly,omitempty"`
	// Plain means any brackets here are really a main line.
	Plain bool `json:"plain,omitempty"`
	// Exact means the times were read from the video's colour wipe, so nothing
	// else may change them.
	Exact bool `json:"exact,omitempty"`
}

// Word is one highlighted unit.
type Word struct {
	Text    string `json:"text"`
	StartMs int64  `json:"startMs"`
	EndMs   int64  `json:"endMs"`
}

// Text returns a line's words joined by spaces.
func (l Line) Text() string {
	parts := make([]string, len(l.Words))
	for i, w := range l.Words {
		parts[i] = w.Text
	}
	return strings.Join(parts, " ")
}

// PlainText returns all the song's words, for searching.
func (d *Doc) PlainText() string {
	var b strings.Builder
	for _, l := range d.Lines {
		b.WriteString(l.Text())
		b.WriteByte('\n')
	}
	return b.String()
}

// ExactLines counts the lines timed from the video's colour wipe.
func (d *Doc) ExactLines() int {
	n := 0
	for _, l := range d.Lines {
		if l.Exact {
			n++
		}
	}
	return n
}

// MostlyFromVideo reports whether at least half the lines have exact times. The
// rest then sit between exact neighbours, which beats listening to a karaoke
// track, where only backing singers can be heard.
func (d *Doc) MostlyFromVideo() bool {
	return len(d.Lines) > 0 && d.ExactLines()*2 >= len(d.Lines)
}

// SetWordTimes gives a line its exact word times, if there's one for each word.
func (l *Line) SetWordTimes(times [][2]int64) bool {
	if len(times) == 0 || len(times) != len(l.Words) {
		return false
	}
	for i, t := range times {
		l.Words[i].StartMs, l.Words[i].EndMs = t[0], t[1]
	}
	l.StartMs, l.EndMs = times[0][0], times[len(times)-1][1]
	l.Exact = true
	return true
}

// FitUntimed moves each run of lines with no exact times between the exact lines
// around it, if the run has strayed outside them.
func FitUntimed(lines []Line) {
	for i := 0; i < len(lines); {
		if lines[i].Exact {
			i++
			continue
		}
		j := i
		for j < len(lines) && !lines[j].Exact {
			j++
		}
		if i > 0 && j < len(lines) {
			from, to := lines[i-1].EndMs, lines[j].StartMs
			first, last := lines[i].StartMs, lines[j-1].EndMs
			if to > from && last > first && (first < from || last > to) {
				place := func(ms int64) int64 { return from + (ms-first)*(to-from)/(last-first) }
				for k := i; k < j; k++ {
					lines[k].Retime(place(lines[k].StartMs), place(lines[k].EndMs))
				}
			}
		}
		i = j
	}
}

// NewLine makes a line from plain text, sharing its time between the words.
func NewLine(text string, startMs, endMs int64) Line {
	return Line{StartMs: startMs, EndMs: endMs, Words: splitWords(text, startMs, endMs)}
}

// Retime moves a line to a new time, keeping its words in proportion.
func (l *Line) Retime(startMs, endMs int64) {
	oldStart, oldSpan := l.StartMs, max(l.EndMs-l.StartMs, 1)
	at := func(ms int64) int64 { return startMs + (ms-oldStart)*(endMs-startMs)/oldSpan }
	for i := range l.Words {
		l.Words[i].StartMs, l.Words[i].EndMs = at(l.Words[i].StartMs), at(l.Words[i].EndMs)
	}
	for i := range l.Backing {
		l.Backing[i].StartMs, l.Backing[i].EndMs = at(l.Backing[i].StartMs), at(l.Backing[i].EndMs)
	}
	l.StartMs, l.EndMs = startMs, endMs
}

// Row is one row of lyrics as a video lays them out: how many words it holds,
// and their exact times if the video showed them.
type Row struct {
	Words     int        `json:"words"`
	WordTimes [][2]int64 `json:"wordTimes,omitempty"`
	Role      string     `json:"role,omitempty"`  // "backing" for backing vocals
	Plain     bool       `json:"plain,omitempty"` // brackets, but really a main line
	Voice     string     `json:"voice,omitempty"` // singer the video's colours show
}

// videoVoices turns the singers a video's colours show into voice parts.
var videoVoices = map[string]Voice{
	"boy":   {ID: "a", Label: "Boy", Source: "video"},
	"girl":  {ID: "b", Label: "Girl", Source: "video"},
	"both":  {ID: "both", Label: "Both", Source: "video"},
	"part1": {ID: "a", Label: "Part 1", Source: "video"},
	"part2": {ID: "b", Label: "Part 2", Source: "video"},
	"part3": {ID: "c", Label: "Part 3", Source: "video"},
}

// maxEchoGapMs is how long after a main line a backing part can still echo it.
const maxEchoGapMs = 3000

// attachBacking puts each backing row with the main line it's sung over, or
// else the one it echoes. One with neither stays as its own small line.
func attachBacking(lines, backing []Line) []Line {
	for _, b := range backing {
		best, most := -1, int64(0)
		for i, l := range lines {
			if shared := min(l.EndMs, b.EndMs) - max(l.StartMs, b.StartMs); shared > most {
				best, most = i, shared
			}
		}
		if best < 0 {
			for i, l := range lines {
				if l.StartMs <= b.StartMs && b.StartMs-l.EndMs <= maxEchoGapMs {
					best = i
				}
			}
		}
		if best < 0 {
			b.BackingOnly = true
			lines = append(lines, b)
			continue
		}
		lines[best].Backing = append(lines[best].Backing, b.Words...)
		end := b.EndMs
		if best+1 < len(lines) {
			end = min(end, lines[best+1].StartMs)
		}
		lines[best].EndMs = max(lines[best].EndMs, end)
	}
	sort.SliceStable(lines, func(i, j int) bool { return lines[i].StartMs < lines[j].StartMs })
	return lines
}

// Reflow rebuilds the lines to match the given rows, keeping every word and its
// timing. It changes nothing unless the rows account for every word, and it
// returns how many rows got exact word times.
func (d *Doc) Reflow(rows []Row) int {
	type placed struct {
		word  Word
		voice string
	}
	var words []placed
	for _, l := range d.Lines {
		for _, w := range l.Words {
			words = append(words, placed{w, l.Voice})
		}
	}
	total := 0
	for _, r := range rows {
		total += r.Words
	}
	if total != len(words) || total == 0 {
		return 0
	}
	var made []Line
	var backing []Line
	voices := map[string]Voice{}
	timed, at := 0, 0
	for _, r := range rows {
		if r.Words <= 0 {
			continue
		}
		part := words[at : at+r.Words]
		at += r.Words
		exact := len(r.WordTimes) == r.Words
		if exact {
			timed++
		}
		seen, shown := videoVoices[r.Voice]
		if shown {
			voices[seen.ID] = seen
		}
		isBacking := r.Role == "backing"
		voiceOf := func(p placed) string {
			switch {
			case isBacking:
				return ""
			case shown:
				return seen.ID
			}
			return p.voice
		}
		// Two singers never share a row.
		start := 0
		for i := 1; i <= len(part); i++ {
			if i < len(part) && voiceOf(part[i]) == voiceOf(part[start]) {
				continue
			}
			line := Line{Voice: voiceOf(part[start]), Plain: r.Plain, Exact: exact}
			for j := start; j < i; j++ {
				w := part[j].word
				if exact {
					w.StartMs, w.EndMs = r.WordTimes[j][0], r.WordTimes[j][1]
				}
				line.Words = append(line.Words, w)
			}
			line.StartMs, line.EndMs = line.Words[0].StartMs, line.Words[len(line.Words)-1].EndMs
			if isBacking {
				backing = append(backing, line)
			} else {
				made = append(made, line)
			}
			start = i
		}
	}
	// Rows stay in lyric order. One with no exact times has to fit between the
	// rows on either side that do.
	FitUntimed(made)
	lines := made
	for i := range lines {
		// The player needs starts in order, whatever the timing said.
		if i > 0 && lines[i].StartMs < lines[i-1].StartMs {
			span := max(lines[i].EndMs-lines[i].StartMs, 200)
			lines[i].Retime(lines[i-1].EndMs, lines[i-1].EndMs+span)
		}
	}
	for i := 0; i+1 < len(lines); i++ {
		if next := lines[i+1].StartMs; lines[i].EndMs > next && next > lines[i].StartMs {
			lines[i].Retime(lines[i].StartMs, next)
		}
	}
	d.Lines = attachBacking(lines, backing)
	if len(voices) > 0 { // the video shows who really sings, which beats any guess
		d.Voices = d.Voices[:0]
		for _, id := range []string{"a", "b", "c", "both"} {
			if v, ok := voices[id]; ok {
				d.Voices = append(d.Voices, v)
			}
		}
	}
	return timed
}

const (
	maxLineWidth = 34 // wider than this and a line has to shrink to fit the screen
	minPartWidth = 7
)

var (
	breakBefore = map[string]bool{"and": true, "but": true, "or": true, "so": true, "when": true, "while": true, "because": true, "'cause": true, "cause": true, "that": true, "if": true, "like": true, "than": true, "to": true}
)

func textWidth(s string) int {
	width := 0
	for _, r := range s {
		width++
		if isCJK(r) {
			width++
		}
	}
	return width
}

// BreakLong splits lines too long to read comfortably, choosing natural places:
// after punctuation or before a joining word, near the middle.
func (d *Doc) BreakLong() {
	var out []Line
	for _, l := range d.Lines {
		out = append(out, breakLine(l)...)
	}
	d.Lines = out
}

// backingWords marks the words inside brackets, which are backing vocals. They
// show on their own small row, so they don't take room on the main one. A close
// with no open means the bracket began on the row before.
func backingWords(words []Word) (flags []bool, inside []bool) {
	flags, inside = make([]bool, len(words)), make([]bool, len(words))
	opened, closed := 0, 0
	for _, w := range words {
		opened += strings.Count(w.Text, "(") + strings.Count(w.Text, "（")
		closed += strings.Count(w.Text, ")") + strings.Count(w.Text, "）")
	}
	depth := 0
	if closed > opened {
		depth = 1
	}
	for i, w := range words {
		opens := strings.Count(w.Text, "(") + strings.Count(w.Text, "（")
		closes := strings.Count(w.Text, ")") + strings.Count(w.Text, "）")
		inside[i] = depth > 0 // a break before this word would split the brackets
		flags[i] = depth > 0 || opens > 0
		depth = max(depth+opens-closes, 0)
	}
	return flags, inside
}

// lineWidths gives each word's room on screen, including the space after it.
func lineWidths(words []Word, backing []bool) []int {
	allBacking := !slices.Contains(backing, false)
	out := make([]int, len(words))
	for i, w := range words {
		switch {
		case !backing[i]:
			out[i] = textWidth(w.Text) + 1
		case allBacking:
			out[i] = textWidth(w.Text)*55/100 + 1
		}
	}
	return out
}

var (
	directionLine = regexp.MustCompile(`(?i)^[\(\[（]\s*(?:pre-?chorus|chorus|verse(?:\s*\d+)?|bridge|intro|outro|hook|refrain|interlude|instrumental|(?:guitar\s+)?solo|music|repeat|fade\s*out|spoken|x?\d+x?)\s*[\)\]）]$`)
	repeatMark    = regexp.MustCompile(`(?i)^[\(\[（]x?\d+x?[\)\]）]$`)
)

// DropDirections removes notes that aren't sung, like "(Chorus)" or "(x2)".
func (d *Doc) DropDirections() {
	kept := d.Lines[:0]
	for _, l := range d.Lines {
		if directionLine.MatchString(strings.TrimSpace(l.Text())) {
			continue
		}
		words := l.Words[:0]
		for _, w := range l.Words {
			if !repeatMark.MatchString(w.Text) {
				words = append(words, w)
			}
		}
		if l.Words = words; len(words) > 0 {
			kept = append(kept, l)
		}
	}
	d.Lines = kept
}

func breakLine(l Line) []Line {
	if len(l.Words) < 2 {
		return []Line{l}
	}
	backing, inside := backingWords(l.Words)
	if l.Plain {
		backing, inside = make([]bool, len(l.Words)), make([]bool, len(l.Words))
	}
	widths := lineWidths(l.Words, backing)
	full := -1
	for _, w := range widths {
		full += w
	}
	if full <= maxLineWidth {
		return []Line{l}
	}
	best, bestCost := 0, math.MaxInt
	width := 0
	for i := 1; i < len(l.Words); i++ {
		width += widths[i-1]
		left, right := width-1, full-width
		cost := abs(left - right)
		if left < minPartWidth || right < minPartWidth {
			cost += 1000
		}
		if inside[i] {
			cost += 2000
		}
		if last := l.Words[i-1].Text; last != "" && strings.ContainsRune(",;:—–!?", []rune(last)[len([]rune(last))-1]) {
			cost -= 18
		}
		if breakBefore[strings.ToLower(l.Words[i].Text)] {
			cost -= 8
		}
		if cost < bestCost {
			best, bestCost = i, cost
		}
	}
	part := func(words []Word) Line {
		return Line{StartMs: words[0].StartMs, EndMs: words[len(words)-1].EndMs, Voice: l.Voice, Words: words, Plain: l.Plain, BackingOnly: l.BackingOnly, Exact: l.Exact}
	}
	second := part(l.Words[best:])
	second.Backing = l.Backing // the backing row follows the end of the line
	return append(breakLine(part(l.Words[:best])), breakLine(second)...)
}

// repairLimitMs is how far a line may disagree with the lines around it.
const repairLimitMs = 6000

// RepairTiming puts back lines that word timing moved far from their
// neighbours, as happens when it latches onto a spoken intro. It returns how
// many lines it repaired.
func RepairTiming(timed, before []Line) int {
	n := len(timed)
	deltas := make([]int64, n)
	for i := range timed {
		deltas[i] = timed[i].StartMs - before[i].StartMs
	}
	jumped := make([]bool, n)
	for i := range timed {
		nearby := slices.Clone(deltas[max(i-5, 0):min(i+6, n)])
		slices.Sort(nearby)
		jumped[i] = !timed[i].Exact && abs64(deltas[i]-nearby[len(nearby)/2]) > repairLimitMs
	}
	// Each repaired line keeps its spacing from the closest line that's right.
	closest := func(i int) (int64, bool) {
		for d := 1; d < n; d++ {
			if j := i + d; j < n && !jumped[j] {
				return deltas[j], true
			}
			if j := i - d; j >= 0 && !jumped[j] {
				return deltas[j], true
			}
		}
		return 0, false
	}
	repaired := 0
	for i := range timed {
		if !jumped[i] {
			continue
		}
		delta, ok := closest(i)
		if !ok {
			continue
		}
		line := before[i]
		line.Words = slices.Clone(line.Words)
		line.Retime(line.StartMs+delta, line.EndMs+delta)
		timed[i] = line
		repaired++
	}
	for i := 0; i+1 < n; i++ {
		if next := timed[i+1].StartMs; timed[i].EndMs > next && next > timed[i].StartMs {
			timed[i].Retime(timed[i].StartMs, next)
		}
	}
	return repaired
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

var lrcTime = regexp.MustCompile(`\[(\d{1,3}):(\d{1,2})(?:[.:](\d{1,3}))?\]`)

// ParseLRC reads line-synced text into a sorted slice of (start, text).
func ParseLRC(text string) []Line {
	type entry struct {
		ms int64
		s  string
	}
	var es []entry
	for raw := range strings.SplitSeq(text, "\n") {
		raw = strings.TrimSpace(raw)
		ms := lrcTime.FindAllStringSubmatch(raw, -1)
		if len(ms) == 0 {
			continue
		}
		body := strings.TrimSpace(lrcTime.ReplaceAllString(raw, ""))
		for _, m := range ms {
			min, _ := strconv.ParseInt(m[1], 10, 64)
			sec, _ := strconv.ParseInt(m[2], 10, 64)
			frac := int64(0)
			if m[3] != "" {
				v, _ := strconv.ParseInt(m[3], 10, 64)
				frac = v * int64(math.Pow10(3-len(m[3])))
			}
			es = append(es, entry{min*60000 + sec*1000 + frac, body})
		}
	}
	sort.SliceStable(es, func(i, j int) bool { return es[i].ms < es[j].ms })
	var lines []Line
	for i, e := range es {
		if e.s == "" {
			continue
		}
		end := e.ms + 5000
		if i+1 < len(es) {
			end = min(es[i+1].ms, e.ms+10000)
		}
		lines = append(lines, Line{StartMs: e.ms, EndMs: end, Words: splitWords(e.s, e.ms, end)})
	}
	return lines
}

func isCJK(r rune) bool {
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul)
}

// tokens splits on spaces and treats each CJK character as its own word.
func tokens(s string) []string {
	var out []string
	for f := range strings.FieldsSeq(s) {
		var cur strings.Builder
		for _, r := range f {
			if isCJK(r) {
				if cur.Len() > 0 {
					out = append(out, cur.String())
					cur.Reset()
				}
				out = append(out, string(r))
				continue
			}
			cur.WriteRune(r)
		}
		if cur.Len() > 0 {
			out = append(out, cur.String())
		}
	}
	return out
}

// splitWords spreads words across a line by character count.
func splitWords(s string, start, end int64) []Word {
	toks := tokens(s)
	total := 0
	for _, t := range toks {
		total += utf8.RuneCountInString(t)
	}
	if total == 0 {
		return nil
	}
	words := make([]Word, 0, len(toks))
	dur := float64(max(end-start, 1))
	seen := 0
	for _, t := range toks {
		a := start + int64(float64(seen)/float64(total)*dur)
		seen += utf8.RuneCountInString(t)
		b := start + int64(float64(seen)/float64(total)*dur)
		words = append(words, Word{Text: t, StartMs: a, EndMs: b})
	}
	return words
}

// Shift moves every time by delta.
func (d *Doc) Shift(delta int64) {
	for i := range d.Lines {
		d.Lines[i].StartMs = max(d.Lines[i].StartMs+delta, 0)
		d.Lines[i].EndMs = max(d.Lines[i].EndMs+delta, 0)
		for _, words := range [][]Word{d.Lines[i].Words, d.Lines[i].Backing} {
			for j := range words {
				words[j].StartMs = max(words[j].StartMs+delta, 0)
				words[j].EndMs = max(words[j].EndMs+delta, 0)
			}
		}
	}
}

// marker matches a part label at the start of a lyric line.
var marker = regexp.MustCompile(`(?i)^\s*[\(\[]?\s*(boy|girl|male|female|both|all|m|f)\s*[:\)\]]+\s*`)

// ApplyMarkers finds part labels in the text, removes them, and assigns voices.
// A label applies to the lines after it until the next label.
func (d *Doc) ApplyMarkers() bool {
	found := map[string]bool{}
	current := ""
	any := false
	for i := range d.Lines {
		line := &d.Lines[i]
		if len(line.Words) == 0 {
			continue
		}
		text := line.Text()
		if m := marker.FindStringSubmatch(text); m != nil {
			any = true
			switch strings.ToLower(m[1]) {
			case "boy", "male", "m":
				current = "a"
			case "girl", "female", "f":
				current = "b"
			default:
				current = "both"
			}
			found[current] = true
			rest := strings.TrimSpace(text[len(m[0]):])
			line.Words = splitWords(rest, line.StartMs, line.EndMs)
		}
		line.Voice = current
	}
	if !any || (!found["a"] && !found["b"]) {
		for i := range d.Lines {
			d.Lines[i].Voice = ""
		}
		return false
	}
	d.Voices = []Voice{{"a", "Boy", "marker"}, {"b", "Girl", "marker"}, {"both", "Both", "marker"}}
	return true
}

// ParseTTML reads an AMLL file. It returns the lines and the generic singer
// IDs in order of first use.
func ParseTTML(r io.Reader) ([]Line, []string, error) {
	dec := xml.NewDecoder(r)
	var lines []Line
	var agents []string
	seen := map[string]bool{}
	var cur *Line
	var word *Word
	inBacking := false
	depthSkip := 0
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if depthSkip > 0 {
				depthSkip++
				continue
			}
			switch t.Name.Local {
			case "p":
				l := Line{}
				for _, a := range t.Attr {
					switch a.Name.Local {
					case "begin":
						l.StartMs = parseClock(a.Value)
					case "end":
						l.EndMs = parseClock(a.Value)
					case "agent":
						l.Voice = a.Value
						if !seen[a.Value] {
							seen[a.Value] = true
							agents = append(agents, a.Value)
						}
					}
				}
				cur = &l
			case "span":
				if cur == nil {
					continue
				}
				role, begin, end := "", "", ""
				for _, a := range t.Attr {
					switch a.Name.Local {
					case "role":
						role = a.Value
					case "begin":
						begin = a.Value
					case "end":
						end = a.Value
					}
				}
				if role == "x-bg" { // the spans inside are backing vocals
					inBacking = true
					continue
				}
				if role != "" || begin == "" { // translation, romanization
					depthSkip = 1
					continue
				}
				word = &Word{StartMs: parseClock(begin), EndMs: parseClock(end)}
			}
		case xml.CharData:
			if word != nil && depthSkip == 0 {
				word.Text += string(t)
			}
		case xml.EndElement:
			if depthSkip > 0 {
				depthSkip--
				continue
			}
			switch t.Name.Local {
			case "span":
				if word != nil && cur != nil {
					word.Text = strings.TrimSpace(word.Text)
					switch {
					case word.Text == "":
					case inBacking:
						cur.Backing = append(cur.Backing, *word)
					default:
						cur.Words = append(cur.Words, *word)
					}
				} else {
					inBacking = false // the closing tag of the backing group
				}
				word = nil
			case "p":
				if cur != nil && len(cur.Words) > 0 {
					lines = append(lines, *cur)
				}
				cur = nil
			}
		}
	}
	return lines, agents, nil
}

func parseClock(s string) int64 {
	s = strings.TrimSuffix(strings.TrimSpace(s), "s")
	parts := strings.Split(s, ":")
	var secs float64
	for _, p := range parts {
		v, err := strconv.ParseFloat(p, 64)
		if err != nil {
			return 0
		}
		secs = secs*60 + v
	}
	return int64(math.Round(secs * 1000))
}

// FindOffset compares lyric line starts with a loudness envelope sampled at
// 10 per second, and returns one time shift. ok is false unless the match is
// clear, in which case the caller leaves the offset at 0.
func FindOffset(envelope []float64, lineStartsMs []int64, maxShiftMs int64) (shiftMs int64, ok bool) {
	if len(envelope) < 100 || len(lineStartsMs) < 4 {
		return 0, false
	}
	onset := make([]float64, len(envelope))
	for i := 2; i < len(envelope); i++ {
		onset[i] = max(envelope[i]-envelope[i-2], 0)
	}
	maxSteps := int(maxShiftMs / 100)
	scores := make([]float64, 2*maxSteps+1)
	for s := -maxSteps; s <= maxSteps; s++ {
		var sum float64
		for _, st := range lineStartsMs {
			i := int(st/100) + s
			if i >= 1 && i+1 < len(onset) {
				sum += onset[i-1] + onset[i] + onset[i+1]
			}
		}
		scores[s+maxSteps] = sum
	}
	best := 0
	var mean float64
	for i, v := range scores {
		mean += v
		if v > scores[best] {
			best = i
		}
	}
	mean /= float64(len(scores))
	var variance, runnerUp float64
	for i, v := range scores {
		variance += (v - mean) * (v - mean)
		if abs(i-best) > 10 && v > runnerUp {
			runnerUp = v
		}
	}
	std := math.Sqrt(variance / float64(len(scores)))
	if std == 0 || scores[best] < mean+4*std || scores[best] < runnerUp*1.15 {
		return 0, false
	}
	return int64(best-maxSteps) * 100, true
}

func abs(a int) int {
	if a < 0 {
		return -a
	}
	return a
}

// LanguageCode shortens a MusicBrainz ISO 639-3 code for Whisper.
func LanguageCode(iso3 string) string {
	m := map[string]string{
		"eng": "en", "tgl": "tl", "fil": "tl", "jpn": "ja", "kor": "ko", "zho": "zh", "cmn": "zh", "yue": "zh",
		"spa": "es", "por": "pt", "fra": "fr", "deu": "de", "ita": "it", "ind": "id", "tha": "th", "vie": "vi", "hin": "hi", "rus": "ru",
	}
	return m[strings.ToLower(iso3)]
}

// LanguageName returns a display name for a two-letter code.
func LanguageName(code string) string {
	m := map[string]string{
		"en": "English", "tl": "Filipino", "ja": "Japanese", "ko": "Korean", "zh": "Chinese", "es": "Spanish",
		"pt": "Portuguese", "fr": "French", "de": "German", "it": "Italian", "id": "Indonesian", "th": "Thai",
		"vi": "Vietnamese", "hi": "Hindi", "ru": "Russian",
	}
	if n, ok := m[code]; ok {
		return n
	}
	return strings.ToUpper(code)
}

// Noise words removed from video titles.
var (
	bracketed = regexp.MustCompile(`(?i)[\(\[\{][^\)\]\}]*(official|video|audio|lyric|lyrics|hd|hq|4k|mv|m/v|karaoke|instrumental|remaster\w*|visuali[sz]er|live)[^\)\]\}]*[\)\]\}]`)
	noise     = regexp.MustCompile(`(?i)\b(official\s+(music\s+)?(video|audio|lyric\s+video)|lyrics?\s+video|with\s+lyrics|lyrics?|karaoke(\s+version)?|instrumental|hd|hq|4k)\b`)
	spaces    = regexp.MustCompile(`\s+`)
	karaokeRe = regexp.MustCompile(`(?i)\b(karaoke|instrumental|minus\s*one|backing\s+track|sing[\s-]*along)\b`)
)

// CleanTitle removes video-title noise.
func CleanTitle(s string) string {
	s = bracketed.ReplaceAllString(s, " ")
	s = noise.ReplaceAllString(s, " ")
	s = spaces.ReplaceAllString(s, " ")
	return strings.Trim(s, " -|~:_")
}

// IsKaraokeTitle detects existing karaoke uploads.
func IsKaraokeTitle(s string) bool { return karaokeRe.MatchString(s) }

// SplitArtistTitle separates "Artist - Title" text, when present.
func SplitArtistTitle(s string) (artist, title string) {
	for _, sep := range []string{" - ", " – ", " — ", " | "} {
		if a, b, ok := strings.Cut(s, sep); ok {
			return strings.TrimSpace(a), strings.TrimSpace(b)
		}
	}
	return "", strings.TrimSpace(s)
}

// CleanUploader turns a channel name into an artist guess.
func CleanUploader(s string) string {
	s = strings.TrimSuffix(s, " - Topic")
	s = strings.TrimSuffix(s, "VEVO")
	return strings.TrimSpace(s)
}

// Similarity is a 0-1 score for comparing short text.
func Similarity(a, b string) float64 {
	ta, tb := wordSet(a), wordSet(b)
	if len(ta) == 0 || len(tb) == 0 {
		return 0
	}
	common := 0
	for w := range ta {
		if tb[w] {
			common++
		}
	}
	return float64(2*common) / float64(len(ta)+len(tb))
}

func wordSet(s string) map[string]bool {
	out := map[string]bool{}
	for _, f := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		out[f] = true
	}
	return out
}

// Fmt is used by tests and logs.
func (l Line) String() string { return fmt.Sprintf("%d-%d %s", l.StartMs, l.EndMs, l.Text()) }
