package documents

import (
	"bytes"
	"regexp"
	"strconv"
	"strings"
)

// TextLayer returns the text each page of pdf shows, in page order, for the evidence check
// (lab-documents.md#validation). It is a small reader for simple text layers, not a renderer:
//
//   - pages are found through the catalog's page tree, in the file body and in FlateDecode
//     object streams;
//   - content streams may be unfiltered or FlateDecode; strings shown between BT and ET are
//     decoded as WinAnsi (the standard fonts' encoding);
//   - a page that shows no text (a scan) yields "".
//
// Limitations (v1): composite fonts (Type0, e.g. Identity-H with a ToUnicode CMap) and custom
// encodings are not decoded, so a document using a Type0 font reports no text layer at all, and
// a custom encoding may make evidence look unverified. Nil means the page tree could not be read.
func TextLayer(pdf []byte) []string {
	objs := pdfObjects(pdf)
	for _, body := range objs {
		if type0Font.Match(body) {
			return nil
		}
	}
	var root []byte
	for _, body := range objs {
		if catalogType.Match(body) {
			root = body
			break
		}
	}
	pagesRef := refAfter(root, "/Pages")
	if pagesRef < 0 {
		return nil
	}
	var pages [][]byte
	seen := map[int]bool{}
	var walk func(n, depth int)
	walk = func(n, depth int) {
		body, ok := objs[n]
		if !ok || seen[n] || depth > 32 {
			return
		}
		seen[n] = true
		if pageType.Match(body) {
			pages = append(pages, body)
			return
		}
		for _, kid := range refsIn(arrayAfter(body, "/Kids")) {
			walk(kid, depth+1)
		}
	}
	walk(pagesRef, 0)
	if len(pages) == 0 {
		return nil
	}
	budget := int64(maxInflated)
	out := make([]string, len(pages))
	for i, page := range pages {
		var contents []int
		if arr := arrayAfter(page, "/Contents"); arr != nil {
			contents = refsIn(arr)
		} else if n := refAfter(page, "/Contents"); n >= 0 {
			if body := objs[n]; bytes.HasPrefix(bytes.TrimSpace(body), []byte("[")) {
				contents = refsIn(body) // an indirect array of streams
			} else {
				contents = []int{n}
			}
		}
		var sb strings.Builder
		for _, n := range contents {
			data, ok := streamData(objs[n], &budget)
			if !ok {
				sb.Reset()
				break // an unsupported filter: treat the page as having no text layer
			}
			showText(data, &sb)
		}
		out[i] = strings.TrimSpace(sb.String())
	}
	return out
}

var (
	objHeader   = regexp.MustCompile(`(\d+)\s+\d+\s+obj\b`)
	catalogType = regexp.MustCompile(`/Type\s*/Catalog(?:[^A-Za-z0-9]|$)`)
	pageType    = regexp.MustCompile(`/Type\s*/Page(?:[^A-Za-z0-9]|$)`)
	type0Font   = regexp.MustCompile(`/Subtype\s*/Type0(?:[^A-Za-z0-9]|$)`)
	refRe       = regexp.MustCompile(`(\d+)\s+\d+\s+R\b`)
	filterRe    = regexp.MustCompile(`/Filter\s*(\[[^\]]*\]|/[A-Za-z0-9]+)`)
	objStmN     = regexp.MustCompile(`/N\s+(\d+)`)
	objStmFirst = regexp.MustCompile(`/First\s+(\d+)`)
)

// pdfObjects maps object numbers to their bodies (dictionary and stream), the last
// definition winning as in an incremental update, plus the objects of object streams.
func pdfObjects(pdf []byte) map[int][]byte {
	objs := map[int][]byte{}
	locs := objHeader.FindAllSubmatchIndex(pdf, -1)
	for i, l := range locs {
		end := len(pdf)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		body := pdf[l[1]:end]
		if e := bytes.LastIndex(body, []byte("endobj")); e >= 0 {
			body = body[:e]
		}
		n, _ := strconv.Atoi(string(pdf[l[2]:l[3]]))
		objs[n] = body
	}
	budget := int64(maxInflated)
	for _, body := range objStreams(objs) {
		data, ok := streamData(body, &budget)
		if !ok {
			continue
		}
		count, first := intAfter(objStmN, body), intAfter(objStmFirst, body)
		if count <= 0 || first <= 0 || first > len(data) {
			continue
		}
		head := strings.Fields(string(data[:first]))
		for k := 0; k+1 < len(head) && k/2 < count; k += 2 {
			n, err1 := strconv.Atoi(head[k])
			off, err2 := strconv.Atoi(head[k+1])
			if err1 != nil || err2 != nil || first+off > len(data) {
				break
			}
			end := len(data)
			if k+3 < len(head) {
				if next, err := strconv.Atoi(head[k+3]); err == nil && first+next <= len(data) && next >= off {
					end = first + next
				}
			}
			if _, ok := objs[n]; !ok {
				objs[n] = data[first+off : end]
			}
		}
	}
	return objs
}

func objStreams(objs map[int][]byte) [][]byte {
	var out [][]byte
	for _, body := range objs {
		if before, _, ok := bytes.Cut(body, []byte("stream")); ok && objStmTag.Match(before) {
			out = append(out, body)
		}
	}
	return out
}

func intAfter(re *regexp.Regexp, b []byte) int {
	m := re.FindSubmatch(b)
	if m == nil {
		return -1
	}
	n, _ := strconv.Atoi(string(m[1]))
	return n
}

// refAfter returns the object number of the reference that follows key, or -1.
func refAfter(body []byte, key string) int {
	i := bytes.Index(body, []byte(key))
	if i < 0 {
		return -1
	}
	rest := bytes.TrimLeft(body[i+len(key):], " \t\r\n")
	m := refRe.FindSubmatchIndex(rest)
	if m == nil || m[0] != 0 {
		return -1
	}
	n, _ := strconv.Atoi(string(rest[m[2]:m[3]]))
	return n
}

// arrayAfter returns the [...] directly following key, or nil.
func arrayAfter(body []byte, key string) []byte {
	i := bytes.Index(body, []byte(key))
	if i < 0 {
		return nil
	}
	rest := bytes.TrimLeft(body[i+len(key):], " \t\r\n")
	if !bytes.HasPrefix(rest, []byte("[")) {
		return nil
	}
	if e := bytes.IndexByte(rest, ']'); e >= 0 {
		return rest[:e+1]
	}
	return nil
}

func refsIn(b []byte) []int {
	var out []int
	for _, m := range refRe.FindAllSubmatch(b, -1) {
		n, _ := strconv.Atoi(string(m[1]))
		out = append(out, n)
	}
	return out
}

// streamData returns the decoded stream of an object body; ok is false for a missing stream
// or a filter other than FlateDecode.
func streamData(body []byte, budget *int64) ([]byte, bool) {
	loc := streamRe.FindIndex(body)
	if loc == nil {
		return nil, false
	}
	dict, data := body[:loc[0]], body[loc[1]:]
	if e := bytes.LastIndex(data, []byte("endstream")); e >= 0 {
		data = data[:e]
	}
	f := filterRe.FindSubmatch(dict)
	if f == nil {
		return data, true
	}
	if names := bytes.Fields(bytes.Trim(f[1], "[]")); len(names) != 1 || string(names[0]) != "/FlateDecode" {
		return nil, false
	}
	out, err := inflate(data, budget)
	return out, err == nil
}

// showText appends the strings shown between BT and ET in a content stream to sb, one space
// between strings. Inline images and comments are skipped.
func showText(c []byte, sb *strings.Builder) {
	inText := false
	for i := 0; i < len(c); {
		switch ch := c[i]; {
		case ch == '%':
			for i < len(c) && c[i] != '\n' && c[i] != '\r' {
				i++
			}
		case ch == '(':
			s, next := literalString(c, i)
			if inText {
				sb.WriteString(winAnsi(s))
				sb.WriteByte(' ')
			}
			i = next
		case ch == '<' && i+1 < len(c) && c[i+1] == '<':
			i += 2
		case ch == '<':
			end := bytes.IndexByte(c[i:], '>')
			if end < 0 {
				return
			}
			if inText {
				sb.WriteString(winAnsi(hexString(c[i+1 : i+end])))
				sb.WriteByte(' ')
			}
			i += end + 1
		case isRegular(ch):
			j := i
			for j < len(c) && isRegular(c[j]) {
				j++
			}
			switch string(c[i:j]) {
			case "BT":
				inText = true
			case "ET":
				inText = false
			case "ID": // inline image data runs to EI
				end := bytes.Index(c[j:], []byte("EI"))
				if end < 0 {
					return
				}
				j += end + 2
			}
			i = j
		default:
			i++
		}
	}
}

func isRegular(b byte) bool {
	return !bytes.ContainsRune([]byte(" \t\r\n\f\x00()<>[]{}/%"), rune(b))
}

// literalString decodes the (...) string starting at c[i] and returns it with the index after it.
func literalString(c []byte, i int) ([]byte, int) {
	var out []byte
	depth := 0
	for i++; i < len(c); i++ {
		switch ch := c[i]; ch {
		case '(':
			depth++
			out = append(out, ch)
		case ')':
			if depth == 0 {
				return out, i + 1
			}
			depth--
			out = append(out, ch)
		case '\\':
			i++
			if i >= len(c) {
				return out, i
			}
			switch e := c[i]; e {
			case 'n':
				out = append(out, '\n')
			case 'r':
				out = append(out, '\r')
			case 't':
				out = append(out, '\t')
			case 'b', 'f':
			case '\r', '\n': // line continuation
				if e == '\r' && i+1 < len(c) && c[i+1] == '\n' {
					i++
				}
			default:
				if e >= '0' && e <= '7' {
					v, k := 0, 0
					for ; k < 3 && i+k < len(c) && c[i+k] >= '0' && c[i+k] <= '7'; k++ {
						v = v*8 + int(c[i+k]-'0')
					}
					i += k - 1
					out = append(out, byte(v))
				} else {
					out = append(out, e)
				}
			}
		default:
			out = append(out, ch)
		}
	}
	return out, i
}

func hexString(h []byte) []byte {
	var digits []byte
	for _, b := range h {
		if (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F') {
			digits = append(digits, b)
		}
	}
	if len(digits)%2 == 1 {
		digits = append(digits, '0')
	}
	out := make([]byte, len(digits)/2)
	for i := range out {
		v, _ := strconv.ParseUint(string(digits[2*i:2*i+2]), 16, 8)
		out[i] = byte(v)
	}
	return out
}

// winAnsiHigh maps the WinAnsiEncoding codes 0x80-0x9F that differ from Latin-1.
var winAnsiHigh = map[byte]rune{0x80: '€', 0x85: '…', 0x91: '‘', 0x92: '’', 0x93: '“', 0x94: '”', 0x95: '•', 0x96: '–', 0x97: '—', 0x99: '™'}

func winAnsi(b []byte) string {
	var sb strings.Builder
	for _, c := range b {
		switch {
		case c >= 0x80 && c <= 0x9f:
			if r, ok := winAnsiHigh[c]; ok {
				sb.WriteRune(r)
			}
		case c < 0x20:
			sb.WriteByte(' ')
		default:
			sb.WriteRune(rune(c)) // ASCII and Latin-1
		}
	}
	return sb.String()
}
