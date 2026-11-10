package lyrics

import (
	"context"
	"math"
	"net/url"
	"regexp"
	"strings"
)

// Guess is one reading of who sings a video and what it's called.
type Guess struct {
	Artist, Title string
}

// NameClues is everything YouTube tells us about a video.
type NameClues struct {
	RawTitle, Track, Artist, Channel string
	DurationMs                       int64
}

var (
	styleOf     = regexp.MustCompile(`(?i)[\(\[\{|]?\s*(?:in the style of|originally (?:performed |sung |recorded )?by|made famous by|as (?:made famous |performed )?by|popularized by|performed by)\s+([^\)\]\}|]+)[\)\]\}]?`)
	titleBy     = regexp.MustCompile(`(?i)^(.+?)\s+by\s+(.+)$`)
	separators  = regexp.MustCompile(`\s+(?:[-–—|~•]+|//)\s+`)
	brandName   = regexp.MustCompile(`(?i)karaoke|karafun|sing\s*king|stingray|singsnap|minus\s*one|backing\s*track|sing[\s-]*along`)
	fillerWords = regexp.MustCompile(`(?i)^(?:(?:female|male|lower|higher|low|high|key|piano|guitar|acoustic|version|cover|with|vocals?|guide|no|tv|music|channel)\s*)+$`)
)

// titleParts splits a video title into its meaningful pieces.
func titleParts(raw, channel string) []string {
	pieces := separators.Split(raw, -1)
	var parts []string
	for _, piece := range pieces {
		cleaned := CleanTitle(piece)
		if cleaned == "" || fillerWords.MatchString(cleaned) || brandName.MatchString(cleaned) {
			continue
		}
		// With several pieces, a leftover word or the channel's name isn't a name.
		if len(pieces) >= 3 {
			if brandName.MatchString(piece) && len(strings.Fields(cleaned)) <= 1 {
				continue
			}
			if channel != "" && Similarity(cleaned, channel) >= 0.8 {
				continue
			}
		}
		parts = append(parts, cleaned)
	}
	return parts
}

// singerScore rates how much a piece of text looks like a singer's name.
func singerScore(name, channel string, known func(string) bool) int {
	score := 0
	if channel != "" && Similarity(name, channel) >= 0.7 {
		score += 3
	}
	if known != nil && known(name) {
		score += 3
	}
	words := len(strings.Fields(name))
	if words <= 4 {
		score++
	}
	if words >= 6 {
		score -= 2
	}
	if strings.ContainsAny(name, "?!") {
		score -= 2
	}
	return score
}

// Guesses lists the possible readings of a video, most likely first.
func Guesses(c NameClues, known func(string) bool) []Guess {
	var out []Guess
	seen := map[string]bool{}
	add := func(artist, title string) {
		artist, title = strings.TrimSpace(artist), strings.TrimSpace(title)
		if key := strings.ToLower(artist + "\x00" + title); title != "" && !seen[key] {
			seen[key] = true
			out = append(out, Guess{artist, title})
		}
	}
	channel := CleanUploader(c.Channel)
	singerChannel := channel
	if brandName.MatchString(channel) {
		singerChannel = ""
	}
	pair := func(a, b string) {
		if singerScore(b, singerChannel, known) > singerScore(a, singerChannel, known) {
			add(b, a)
			add(a, b)
			return
		}
		add(a, b)
		add(b, a)
	}

	if c.Track != "" { // YouTube's own music details
		add(c.Artist, CleanTitle(c.Track))
	}
	raw := c.RawTitle
	if m := styleOf.FindStringSubmatch(raw); m != nil {
		if rest := titleParts(strings.Replace(raw, m[0], " ", 1), channel); len(rest) > 0 {
			add(CleanTitle(m[1]), rest[0])
		}
		raw = strings.Replace(raw, m[0], " ", 1)
	}
	parts := titleParts(raw, channel)
	switch {
	case len(parts) >= 3:
		pair(parts[0], parts[1])
		pair(parts[1], parts[2])
		pair(parts[0], parts[2])
	case len(parts) == 2:
		pair(parts[0], parts[1])
	case len(parts) == 1:
		if m := titleBy.FindStringSubmatch(parts[0]); m != nil {
			add(m[2], m[1])
		}
		if singerChannel != "" {
			add(singerChannel, parts[0])
		}
		add("", parts[0])
	}
	return out
}

// ConfirmNames asks the lyric database, whose search doesn't care about word
// order, which reading is real. It returns the database's own spelling.
func ConfirmNames(ctx context.Context, guesses []Guess, durationMs int64) (Guess, bool) {
	if len(guesses) == 0 {
		return Guess{}, false
	}
	var results []LrcResult
	search := strings.TrimSpace(guesses[0].Artist + " " + guesses[0].Title)
	if _, err := getJSON(ctx, LRCLIBBase+"/api/search?"+url.Values{"q": {search}}.Encode(), &results); err != nil {
		return Guess{}, false
	}
	return pickConfirmed(results, guesses, durationMs)
}

func pickConfirmed(results []LrcResult, guesses []Guess, durationMs int64) (Guess, bool) {
	var best *LrcResult
	bestScore := 0.0
	for i := range results {
		r := &results[i]
		for _, g := range guesses {
			titleMatch := Similarity(r.TrackName, g.Title)
			artistMatch := Similarity(r.ArtistName, g.Artist)
			var s float64
			switch {
			case g.Artist == "" && titleMatch >= 0.9:
				s = titleMatch * 1.3 // the title alone can only just qualify
			case titleMatch >= 0.6 && artistMatch >= 0.6:
				s = titleMatch + artistMatch
			default:
				continue
			}
			if variantTitle.MatchString(r.TrackName) && !variantTitle.MatchString(g.Title) {
				s -= 0.3
			}
			if durationMs > 0 && r.Duration > 0 {
				s += 0.3 * math.Max(0, 1-math.Abs(r.Duration-float64(durationMs)/1000)/30)
			}
			if s > bestScore {
				best, bestScore = r, s
			}
		}
	}
	if best == nil || bestScore < 1.15 {
		return Guess{}, false
	}
	return Guess{Artist: best.ArtistName, Title: best.TrackName}, true
}
