package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/image/font"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

// scriptWord is the word drawn in the optional script font, in the name.
const scriptWord = "dock"

// outlineCache keeps the outline until the font file changes.
var outlineCache struct {
	sync.Mutex
	mod  time.Time
	body []byte
}

// scriptOutline sends the letter shapes of "dock" in the optional script
// font, so pages can trace its edges without reading the font themselves.
// Pages use shapes saved at build time for the built-in font.
func (s *Server) scriptOutline(w http.ResponseWriter, r *http.Request) {
	path := filepath.Join(s.MetadataDir, "fonts", "script.otf")
	st, err := os.Stat(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	outlineCache.Lock()
	defer outlineCache.Unlock()
	if outlineCache.body == nil || !outlineCache.mod.Equal(st.ModTime()) {
		raw, err := os.ReadFile(path)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		body, err := wordOutline(raw, scriptWord)
		if err != nil {
			writeErr(w, http.StatusUnprocessableEntity, "Couldn't read the script font.")
			return
		}
		outlineCache.mod, outlineCache.body = st.ModTime(), body
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, "outline.json", outlineCache.mod, strings.NewReader(string(outlineCache.body)))
}

// wordOutline returns {"em": units per em, "d": SVG path} for a word, drawn at
// one em per unit with the baseline at y=0, letters spaced and kerned.
func wordOutline(raw []byte, word string) ([]byte, error) {
	f, err := sfnt.Parse(raw)
	if err != nil {
		return nil, err
	}
	var b sfnt.Buffer
	em := f.UnitsPerEm()
	ppem := fixed.I(int(em)) // so one unit is one font unit
	var d strings.Builder
	pt := func(p fixed.Point26_6, x fixed.Int26_6) string {
		return fmt.Sprintf("%.1f %.1f", float64(p.X+x)/64, float64(p.Y)/64)
	}
	x := fixed.Int26_6(0)
	prev := sfnt.GlyphIndex(0)
	for i, ch := range word {
		g, err := f.GlyphIndex(&b, ch)
		if err != nil {
			return nil, err
		}
		if i > 0 {
			if k, err := f.Kern(&b, prev, g, ppem, font.HintingNone); err == nil {
				x += k
			}
		}
		segs, err := f.LoadGlyph(&b, g, ppem, nil)
		if err != nil {
			return nil, err
		}
		for _, sg := range segs {
			a := sg.Args
			switch sg.Op {
			case sfnt.SegmentOpMoveTo:
				if d.Len() > 0 {
					d.WriteString("Z")
				}
				d.WriteString("M" + pt(a[0], x))
			case sfnt.SegmentOpLineTo:
				d.WriteString("L" + pt(a[0], x))
			case sfnt.SegmentOpQuadTo:
				d.WriteString("Q" + pt(a[0], x) + " " + pt(a[1], x))
			case sfnt.SegmentOpCubeTo:
				d.WriteString("C" + pt(a[0], x) + " " + pt(a[1], x) + " " + pt(a[2], x))
			}
		}
		adv, err := f.GlyphAdvance(&b, g, ppem, font.HintingNone)
		if err != nil {
			return nil, err
		}
		x += adv
		prev = g
	}
	if d.Len() > 0 {
		d.WriteString("Z")
	}
	return json.Marshal(map[string]any{"em": int(em), "d": d.String()})
}
