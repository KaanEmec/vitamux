package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"math/rand/v2"
	"strings"

	"golang.org/x/image/font/basicfont"
)

// A minimal PDF 1.4 writer for the synthetic lab reports: standard Type 1 fonts (no embedding),
// uncompressed content streams so the text layer is plain to inspect, and for the "scanned"
// variant one 1-bit image per page with RunLengthDecode (no zlib, so output never depends on
// the Go version's flate). All coordinates are integer points, origin bottom left.

// canvas is one page being drawn. pdfCanvas writes a text layer; rasterCanvas draws the same
// layout into a bitmap with no text layer.
type canvas interface {
	text(x, y, size int, bold bool, s string)
	rule(x0, y0, x1, y1 int)
	smudge(x0, y0, x1, y1 int) // makes an area unreadable
	width(s string, size int, bold bool) int
	lineBox(size int) (ascent, descent int)
}

// ---- text layer ----

type pdfCanvas struct{ buf bytes.Buffer }

// Helvetica advance widths (1/1000 em) for ASCII 32..126, from the standard AFM.
var helvetica = [95]int{
	278, 278, 355, 556, 556, 889, 667, 191, 333, 333, 389, 584, 278, 333, 278, 278,
	556, 556, 556, 556, 556, 556, 556, 556, 556, 556, 278, 278, 584, 584, 584, 556,
	1015, 667, 667, 722, 722, 667, 611, 778, 722, 278, 500, 667, 556, 833, 722, 778,
	667, 778, 722, 667, 611, 722, 667, 944, 667, 667, 611, 278, 278, 278, 469, 556,
	333, 556, 556, 500, 556, 556, 278, 556, 556, 222, 222, 500, 222, 833, 556, 556,
	556, 556, 333, 500, 278, 556, 500, 722, 500, 500, 500, 334, 260, 334, 584,
}

// WinAnsiEncoding codes for the non-ASCII characters the reports use; symbolFont ones are drawn
// with the Symbol font.
var (
	winAnsi    = map[rune]byte{'µ': 0xB5, 'ü': 0xFC, 'ä': 0xE4, 'ö': 0xF6, 'ß': 0xDF, '²': 0xB2, '–': 0x96, '·': 0xB7}
	symbolFont = map[rune]byte{'≤': 0xA3, '≥': 0xB3, '↑': 0xAD, '↓': 0xAF}
)

func (c *pdfCanvas) text(x, y, size int, bold bool, s string) {
	font := "/F1"
	if bold {
		font = "/F2"
	}
	fmt.Fprintf(&c.buf, "BT %d %d Td", x, y)
	cur, run := "", []byte{}
	flush := func() {
		if len(run) > 0 {
			fmt.Fprintf(&c.buf, " %s %d Tf (%s) Tj", cur, size, pdfEscape(run))
			run = run[:0]
		}
	}
	for _, r := range s {
		f, b := font, byte(r) //nolint:gosec // ASCII only in this branch; others are mapped below
		switch {
		case r >= 32 && r < 127:
		case winAnsi[r] != 0:
			b = winAnsi[r]
		case symbolFont[r] != 0:
			f, b = "/F3", symbolFont[r]
		default:
			panic(fmt.Sprintf("labpdf: no glyph for %q", r))
		}
		if f != cur {
			flush()
			cur = f
		}
		run = append(run, b)
	}
	flush()
	c.buf.WriteString(" ET\n")
}

func pdfEscape(b []byte) string {
	var sb strings.Builder
	for _, c := range b {
		switch c {
		case '(', ')', '\\':
			sb.WriteByte('\\')
			sb.WriteByte(c)
		default:
			if c < 32 || c > 126 {
				fmt.Fprintf(&sb, "\\%03o", c)
			} else {
				sb.WriteByte(c)
			}
		}
	}
	return sb.String()
}

func (c *pdfCanvas) rule(x0, y0, x1, y1 int) {
	fmt.Fprintf(&c.buf, "0.5 w %d %d m %d %d l S\n", x0, y0, x1, y1)
}

func (c *pdfCanvas) smudge(x0, y0, x1, y1 int) {
	fmt.Fprintf(&c.buf, "0.2 g %d %d %d %d re f 0 g\n", x0, y0, x1-x0, y1-y0)
}

// width returns the advance of s in 1/1000 pt. Bold is approximated with the regular metrics.
func (c *pdfCanvas) width(s string, size int, _ bool) int {
	w := 0
	for _, r := range s {
		switch {
		case r >= 32 && r < 127:
			w += helvetica[r-32]
		case symbolFont[r] != 0:
			w += 549
		default:
			w += 556
		}
	}
	return w * size
}

func (c *pdfCanvas) lineBox(size int) (int, int) { return size * 760, size * 240 }

// ---- raster ("scanned") ----

// rasterCanvas draws at 2 px per point with the 7x13 basic font doubled, so one character is
// exactly 7 x 13 pt. Only ASCII is available.
type rasterCanvas struct {
	img  *image.Gray
	h    int // page height in points
	rand *rand.Rand
}

const rasterScale = 2

func newRasterCanvas(w, h int, r *rand.Rand) *rasterCanvas {
	img := image.NewGray(image.Rect(0, 0, w*rasterScale, h*rasterScale))
	for i := range img.Pix {
		img.Pix[i] = 0xff
	}
	return &rasterCanvas{img: img, h: h, rand: r}
}

func (c *rasterCanvas) px(x, y int) (int, int) { return x * rasterScale, (c.h - y) * rasterScale }

func (c *rasterCanvas) text(x, y, _ int, bold bool, s string) {
	face := basicfont.Face7x13
	px, py := c.px(x, y)
	for i, r := range s {
		if r < 32 || r > 126 {
			panic(fmt.Sprintf("labpdf: raster font has no glyph for %q", r))
		}
		gx := px + i*face.Advance*rasterScale
		// Glyph cell: Ascent pixels above the baseline, Descent below.
		top := (int(r) - 32) * face.Height
		for dy := range face.Height {
			for dx := range face.Width {
				if face.Mask.(*image.Alpha).AlphaAt(dx, top+dy).A < 0x80 {
					continue
				}
				for k := range rasterScale {
					for l := range rasterScale {
						c.set(gx+dx*rasterScale+l, py+(dy-face.Ascent)*rasterScale+k)
						if bold {
							c.set(gx+dx*rasterScale+l+1, py+(dy-face.Ascent)*rasterScale+k)
						}
					}
				}
			}
		}
	}
}

func (c *rasterCanvas) set(x, y int) { c.img.SetGray(x, y, color.Gray{}) }

func (c *rasterCanvas) rule(x0, y0, x1, y1 int) {
	ax, ay := c.px(x0, y0)
	bx, by := c.px(x1, y1)
	for x := min(ax, bx); x <= max(ax, bx); x++ {
		for y := min(ay, by); y <= max(ay, by); y++ {
			c.set(x, y)
		}
	}
}

// smudge covers the area with dense ink blots, like a stain on a scanned page.
func (c *rasterCanvas) smudge(x0, y0, x1, y1 int) {
	ax, ay := c.px(x0, y1)
	bx, by := c.px(x1, y0)
	for y := ay; y < by; y++ {
		for x := ax; x < bx; x++ {
			if c.rand.IntN(10) < 7 {
				c.set(x, y)
			}
		}
	}
}

func (c *rasterCanvas) width(s string, _ int, _ bool) int { return len(s) * 7000 }

func (c *rasterCanvas) lineBox(int) (int, int) { return 11000, 2000 }

// speckle adds sparse scanner noise.
func (c *rasterCanvas) speckle() {
	b := c.img.Bounds()
	for range b.Dx() * b.Dy() / 6000 {
		c.set(c.rand.IntN(b.Dx()), c.rand.IntN(b.Dy()))
	}
}

// bits packs the image as 1 bit per pixel, 1 = white, rows padded to a byte.
func (c *rasterCanvas) bits() []byte {
	b := c.img.Bounds()
	stride := (b.Dx() + 7) / 8
	out := make([]byte, stride*b.Dy())
	for y := range b.Dy() {
		for x := range b.Dx() {
			if c.img.Pix[y*c.img.Stride+x] >= 0x80 {
				out[y*stride+x/8] |= 0x80 >> (x % 8)
			}
		}
		for x := b.Dx(); x < stride*8; x++ { // padding stays white
			out[y*stride+x/8] |= 0x80 >> (x % 8)
		}
	}
	return out
}

// runLength encodes src with the PDF RunLengthDecode filter (PackBits).
func runLength(src []byte) []byte {
	var out []byte
	for i := 0; i < len(src); {
		j := i + 1
		for j < len(src) && src[j] == src[i] && j-i < 128 {
			j++
		}
		if j-i >= 2 {
			out = append(out, byte(257-(j-i)), src[i]) //nolint:gosec // run length 2..128
			i = j
			continue
		}
		k := i + 1
		for k < len(src) && k-i < 128 && (k+1 >= len(src) || src[k] != src[k+1]) {
			k++
		}
		out = append(out, byte(k-i-1)) //nolint:gosec // literal length 1..128
		out = append(out, src[i:k]...)
		i = k
	}
	return append(out, 128)
}

// ---- document ----

type pdfPage struct {
	content []byte
	image   *rasterCanvas // set for scanned pages; content then only places the image
}

// writePDF serializes pages of w x h points. The marker comment keeps tools/fixtureguard happy
// when the file has no NUL byte; a .synthetic sidecar covers the binary case.
func writePDF(w, h int, title string, pages []pdfPage) []byte {
	var out bytes.Buffer
	var offsets []int
	obj := func(body string, stream []byte) int {
		offsets = append(offsets, out.Len())
		n := len(offsets)
		if stream == nil {
			fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", n, body)
		} else {
			fmt.Fprintf(&out, "%d 0 obj\n<< %s /Length %d >>\nstream\n", n, body, len(stream))
			out.Write(stream)
			out.WriteString("\nendstream\nendobj\n")
		}
		return n
	}
	out.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n% synthetic: true\n")

	// Objects 1-6 have fixed numbers; pages follow, three objects each at most.
	kids := make([]string, len(pages))
	next := 7
	for i, p := range pages {
		kids[i] = fmt.Sprintf("%d 0 R", next)
		next += 2
		if p.image != nil {
			next++
		}
	}
	obj("<< /Type /Catalog /Pages 2 0 R >>", nil)
	obj(fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), len(pages)), nil)
	obj(fmt.Sprintf("<< /Title (%s) /Subject (synthetic: true) /Producer (vitamux fixturegen labpdf) >>", pdfEscape([]byte(title))), nil)
	obj("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>", nil)
	obj("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica-Bold /Encoding /WinAnsiEncoding >>", nil)
	obj("<< /Type /Font /Subtype /Type1 /BaseFont /Symbol >>", nil)
	for _, p := range pages {
		n := len(offsets) + 1
		if p.image == nil {
			obj(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %d %d] /Resources << /Font << /F1 4 0 R /F2 5 0 R /F3 6 0 R >> >> /Contents %d 0 R >>", w, h, n+1), nil)
			obj("", p.content)
			continue
		}
		obj(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %d %d] /Resources << /XObject << /Im1 %d 0 R >> >> /Contents %d 0 R >>", w, h, n+2, n+1), nil)
		obj("", fmt.Appendf(nil, "q %d 0 0 %d 0 0 cm /Im1 Do Q", w, h))
		b := p.image.img.Bounds()
		obj(fmt.Sprintf("/Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceGray /BitsPerComponent 1 /Filter /RunLengthDecode", b.Dx(), b.Dy()),
			runLength(p.image.bits()))
	}
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(offsets)+1)
	for _, o := range offsets {
		fmt.Fprintf(&out, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R /Info 3 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets)+1, xref)
	return out.Bytes()
}
