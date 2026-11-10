package api

import (
	"fmt"
	"math"
	"net/http"
	"strings"

	qrcode "github.com/skip2/go-qrcode"
)

// qrSVG serves the join QR as halftone dots in the brand colours. It uses the
// highest error correction, so the round dots still scan easily.
func (s *Server) qrSVG(w http.ResponseWriter, _ *http.Request) {
	svg, err := halftoneQR(s.Player.JoinURL())
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(svg))
}

// Dots are never smaller than this share of a square, so every one still reads.
const (
	qrDotMin = 0.62
	qrDotMax = 0.9
)

func halftoneQR(text string) (string, error) {
	q, err := qrcode.New(text, qrcode.High)
	if err != nil {
		return "", err
	}
	q.DisableBorder = true
	bits := q.Bitmap()
	n := len(bits)
	finder := func(x, y int) bool {
		in := func(a int) bool { return a < 7 }
		far := func(a int) bool { return a >= n-7 }
		return (in(x) && in(y)) || (far(x) && in(y)) || (in(x) && far(y))
	}

	var b strings.Builder
	const pad = 1
	size := n + pad*2
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="geometricPrecision">`, size, size)
	// Dark tints of the brand pink and violet, dark enough for cameras.
	fmt.Fprintf(&b, `<defs><linearGradient id="g" gradientUnits="userSpaceOnUse" x1="0" y1="0" x2="%d" y2="%d">`+
		`<stop offset="0" stop-color="#c4185f"/><stop offset="1" stop-color="#5b37d6"/></linearGradient></defs>`, size, size)
	fmt.Fprintf(&b, `<rect width="%d" height="%d" fill="#fff"/><g fill="url(#g)">`, size, size)

	// The three corner squares stay solid, with soft corners, so phones lock on fast.
	for _, c := range [][2]int{{0, 0}, {n - 7, 0}, {0, n - 7}} {
		x, y := float64(c[0]+pad), float64(c[1]+pad)
		fmt.Fprintf(&b, `<path fill-rule="evenodd" d="%s%s"/>`, roundRect(x, y, 7, 1.9), roundRect(x+1, y+1, 5, 1.1))
		fmt.Fprintf(&b, `<path d="%s"/>`, roundRect(x+2, y+2, 3, .8))
	}

	// The rest are dots, largest in the middle and smaller towards the edges,
	// like a halftone print.
	mid := float64(n-1) / 2
	for y, row := range bits {
		for x, on := range row {
			if !on || finder(x, y) {
				continue
			}
			d := math.Hypot(float64(x)-mid, float64(y)-mid) / (mid * math.Sqrt2)
			r := (qrDotMax - (qrDotMax-qrDotMin)*d) / 2
			fmt.Fprintf(&b, `<circle cx="%.1f" cy="%.1f" r="%.3f"/>`, float64(x+pad)+.5, float64(y+pad)+.5, r)
		}
	}
	b.WriteString(`</g></svg>`)
	return b.String(), nil
}

// roundRect is a square path with rounded corners.
func roundRect(x, y, side, r float64) string {
	e := side - 2*r
	return fmt.Sprintf("M%g %gh%ga%g %g 0 0 1 %g %gv%ga%g %g 0 0 1 %g %gh%ga%g %g 0 0 1 %g %gv%ga%g %g 0 0 1 %g %gz",
		x+r, y, e, r, r, r, r, e, r, r, -r, r, -e, r, r, -r, -r, -e, r, r, r, -r)
}
