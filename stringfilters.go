package mist

import (
	"encoding/base64"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// unescaper is liquidjs's unescape, which knows only the entities escape produces.
var unescaper = strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">", "&#34;", `"`, "&#39;", "'")

// units splits s into UTF-16 code units, bailing where one would be half a surrogate pair.
func units(s string, at int) []rune {
	if !utf8.ValidString(s) {
		bail(at, "invalid-utf8", "string filter on invalid UTF-8")
	}
	rs := []rune(s)
	for _, c := range rs {
		if c > 0xFFFF {
			bail(at, "split-surrogate-pair", "string filter splitting a character outside the BMP")
		}
	}
	return rs
}

// sub16 is s.slice(i, j) in UTF-16 code units, bailing where that would split a surrogate pair.
func sub16(s string, i, j, at int) string {
	if !utf8.ValidString(s) {
		bail(at, "invalid-utf8", "string filter on invalid UTF-8")
	}
	if i >= j {
		return ""
	}
	return s[off16(s, i, at):off16(s, j, at)]
}

// off16 is the byte offset of UTF-16 index i in s.
func off16(s string, i, at int) int {
	n := 0
	for b, c := range s {
		if n == i {
			return b
		}
		n++
		if c > 0xFFFF {
			if n == i {
				bail(at, "split-surrogate-pair", "string filter splitting a character outside the BMP")
			}
			n++
		}
	}
	return len(s)
}

// len16 is JavaScript's string length.
func len16(s string) int {
	n := 0
	for _, c := range s {
		n++
		if c > 0xFFFF {
			n++
		}
	}
	return n
}

// caseMap is JavaScript's toUpperCase or toLowerCase, bailing on characters Go maps differently.
func caseMap(s string, upper bool, at int) string {
	for _, c := range s {
		if upper && jsUpperDiffers(c) || !upper && jsLowerDiffers(c) {
			bail(at, "case-mapping", "case mapping of %q, whose JavaScript mapping differs", c)
		}
	}
	if upper {
		return strings.Map(unicode.ToUpper, s)
	}
	return strings.Map(unicode.ToLower, s)
}

// replace is str.split(p).join(r).
func replace(s, p, r string, at int) string {
	if p != "" {
		return strings.ReplaceAll(s, p, r)
	}
	us := units(s, at)
	var b strings.Builder
	for i, c := range us {
		if i > 0 {
			b.WriteString(r)
		}
		b.WriteRune(c)
	}
	return b.String()
}

// strip trims whitespace as String.prototype.trim does, or the characters of a truthy argument.
func strip(v val, a []val, at int, left, right bool) string {
	s := toStr(v, at)
	cut := jsSpace
	if len(a) == 1 && jsTruthy(a[0], at) {
		chars := argStr(a, 0, at)
		units(chars, at)
		cut = func(c rune) bool { return strings.ContainsRune(chars, c) }
	}
	if left {
		s = strings.TrimLeftFunc(s, cut)
	}
	if right {
		s = strings.TrimRightFunc(s, cut)
	}
	return s
}

// truncate is liquidjs's truncate: v itself when it fits, else a prefix plus the ellipsis.
func truncate(v val, a []val, at int) val {
	s := toStr(v, at)
	l := argNum(a, 0, 50, at)
	o := "..."
	if len(a) > 1 {
		o = argOrDefault(a[1], o, at)
	}
	if float64(len16(s)) <= l {
		return v
	}
	return strVal(sub16(s, 0, jsIndex(l-float64(len16(o)), len16(s)), at) + o)
}

// truncatewords is liquidjs's truncatewords, including its ellipsis when the count is exact.
func truncatewords(v val, a []val, at int) string {
	s := toStr(v, at)
	words := argNum(a, 0, 15, at)
	o := "..."
	if len(a) > 1 {
		o = argOrDefault(a[1], o, at)
	}
	// str.split(/\s+/): leading or trailing whitespace yields an empty first or last word.
	var arr []string
	start, inSpace := 0, false
	for i, c := range s {
		switch {
		case jsSpace(c) && !inSpace:
			arr, inSpace = append(arr, s[start:i]), true
		case !jsSpace(c) && inSpace:
			start, inSpace = i, false
		}
	}
	if inSpace {
		arr = append(arr, "")
	} else {
		arr = append(arr, s[start:])
	}
	if words <= 0 {
		words = 1
	}
	ret := strings.Join(arr[:jsIndex(words, len(arr))], " ")
	if float64(len(arr)) >= words {
		ret += o
	}
	return ret
}

// percentEncode is encodeURIComponent or encodeURI: UTF-8 bytes as %XX, except
// alphanumerics, the safe punctuation and, with plus, spaces as +.
func percentEncode(s, safe string, plus bool, at int) string {
	if !utf8.ValidString(s) {
		bail(at, "invalid-utf8", "url_encode of invalid UTF-8")
	}
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', strings.IndexByte(safe, c) >= 0:
			b.WriteByte(c)
		case c == ' ' && plus:
			b.WriteByte('+')
		default:
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&15])
		}
	}
	return b.String()
}

// replaceNewlines is s.replace(/\r?\n/g, repl).
func replaceNewlines(s, repl string) string {
	if strings.IndexByte(s, '\n') < 0 {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '\r' && i+1 < len(s) && s[i+1] == '\n':
		case s[i] == '\n':
			b.WriteString(repl)
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// stripHTML is liquidjs's strip_html, which drops script, style and comment blocks, then tags.
func stripHTML(s string, at int) string {
	if strings.IndexByte(s, '<') < 0 {
		return s
	}
	blocks := []struct{ open, close string }{{"<script", "</script>"}, {"<style", "</style>"}, {"<!--", "-->"}, {"<", ">"}}
	var b strings.Builder
	for i := 0; i < len(s); {
		lt := strings.IndexByte(s[i:], '<')
		if lt < 0 {
			b.WriteString(s[i:])
			break
		}
		lt += i
		b.WriteString(s[i:lt])
		next := i
		for k := 0; k < len(blocks); k++ {
			bl := blocks[k]
			if !strings.HasPrefix(s[lt:], bl.open) {
				continue
			}
			if e := strings.Index(s[lt+len(bl.open):], bl.close); e >= 0 {
				next = lt + len(bl.open) + e + len(bl.close)
				break
			}
			blocks = slices.Delete(blocks, k, k+1)
			k--
		}
		switch {
		case next != i:
			i = next
		case i == lt:
			b.WriteString(s[lt:])
			return b.String()
		default:
			bail(at, "strip-html-unclosed", "strip_html of an unclosed '<' after text, which loops forever in liquidjs")
		}
	}
	return b.String()
}

// urlDecode is decodeURIComponent, then + as a space; malformed escapes or UTF-8 bail,
// as decodeURIComponent throws.
func urlDecode(s string, at int) string {
	if strings.IndexByte(s, '%') < 0 {
		return strings.ReplaceAll(s, "+", " ")
	}
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '%' {
			b = append(b, s[i])
			continue
		}
		if i+2 >= len(s) || !isHex(s[i+1]) || !isHex(s[i+2]) {
			bail(at, "url-decode-malformed", "url_decode of a malformed escape")
		}
		hi, lo := strings.IndexByte("0123456789abcdef", s[i+1]|0x20), strings.IndexByte("0123456789abcdef", s[i+2]|0x20)
		b = append(b, byte(hi<<4|lo))
		i += 2
	}
	if !utf8.Valid(b) {
		bail(at, "invalid-utf8", "url_decode to invalid UTF-8")
	}
	return strings.ReplaceAll(string(b), "+", " ")
}

func isHex(c byte) bool { return isDigit(c) || c|0x20 >= 'a' && c|0x20 <= 'f' }

// base64Decode is Node's lenient Buffer.from(s, 'base64'): standard and URL-safe
// alphabets, everything else skipped, stopping at the first '='. Bytes that aren't
// UTF-8 bail rather than reproduce the replacement characters.
func base64Decode(s string, at int) string {
	var clean []byte
	for i := 0; i < len(s) && s[i] != '='; i++ {
		switch c := s[i]; {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '+', c == '/':
			clean = append(clean, c)
		case c == '-':
			clean = append(clean, '+')
		case c == '_':
			clean = append(clean, '/')
		}
	}
	if len(clean)%4 == 1 {
		clean = clean[:len(clean)-1] // Node drops a lone trailing sextet
	}
	out, err := base64.RawStdEncoding.DecodeString(string(clean))
	if err != nil || !utf8.Valid(out) {
		bail(at, "invalid-utf8", "base64_decode to bytes that aren't UTF-8")
	}
	return string(out)
}

// replaceRuns replaces each run of characters in set with sep.
func replaceRuns(s string, sep byte, set func(rune) bool) string {
	var b strings.Builder
	b.Grow(len(s))
	run := false
	for _, c := range s {
		switch {
		case !set(c):
			b.WriteRune(c)
			run = false
		case !run:
			b.WriteByte(sep)
			run = true
		}
	}
	return b.String()
}

// numberOfWords is liquidjs's number_of_words: whitespace-separated words, or with
// mode cjk (or auto, when s has any) each CJK character as a word of its own.
func numberOfWords(s, mode string) int {
	cjk := mode == "cjk" || mode == "auto" && strings.ContainsFunc(s, isCJK)
	n, inWord := 0, false
	for _, c := range s {
		switch {
		case cjk && isCJK(c):
			n++
			inWord = false
		case jsSpace(c):
			inWord = false
		case !inWord:
			n++
			inWord = true
		}
	}
	return n
}

func isCJK(c rune) bool {
	return c >= 0x4E00 && c <= 0x9FFF || c >= 0xF900 && c <= 0xFAFF || c >= 0x3400 && c <= 0x4DBF ||
		c >= 0x3040 && c <= 0x30FF || c >= 0xAC00 && c <= 0xD7AF
}

var removeAccents = strings.NewReplacer(
	"à", "a", "á", "a", "â", "a", "ã", "a", "ä", "a", "å", "a", "æ", "ae", "ç", "c", "è", "e", "é", "e", "ê", "e", "ë", "e",
	"ì", "i", "í", "i", "î", "i", "ï", "i", "ð", "d", "ñ", "n", "ò", "o", "ó", "o", "ô", "o", "õ", "o", "ö", "o", "ø", "o",
	"ù", "u", "ú", "u", "û", "u", "ü", "u", "ý", "y", "ÿ", "y", "ß", "ss", "œ", "oe", "þ", "th", "ẞ", "SS", "Œ", "OE", "Þ", "TH")

// slugify is liquidjs's slugify: runs of characters the mode doesn't keep become one
// "-", then a leading and a trailing "-" go, and the result is lower-cased unless cased.
func slugify(s string, a []val, at int) string {
	mode := "default"
	if len(a) > 0 {
		mode = argOrDefault(a[0], mode, at)
	}
	s = validStr(s, at)
	var keep func(rune) bool
	switch mode {
	case "raw":
		keep = func(c rune) bool { return !jsSpace(c) }
	case "default", "latin", "pretty":
		pretty := mode == "pretty"
		keep = func(c rune) bool {
			if !unicode.In(c, unicode.L, unicode.M, unicode.N, unicode.P, unicode.S, unicode.Z, unicode.Cc, unicode.Cf, unicode.Co) {
				bail(at, "unicode-version", "slugify of %q, unassigned in Go's Unicode version", c)
			}
			return unicode.In(c, unicode.L, unicode.M, unicode.Nd) || pretty && strings.ContainsRune("._~!$&'()+,;=@", c)
		}
		if mode == "latin" {
			s = removeAccents.Replace(s)
		}
	case "ascii":
		keep = func(c rune) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' }
	case "none":
	default:
		bail(at, "slugify-mode", "slugify mode %q", mode)
	}
	if keep != nil {
		s = replaceRuns(s, '-', func(c rune) bool { return !keep(c) })
		s = strings.TrimPrefix(s, "-")
		s = strings.TrimSuffix(s, "-")
	}
	if len(a) > 1 && jsTruthy(a[1], at) {
		return s
	}
	return caseMap(s, false, at)
}

// jsonStr is appendJSON as a string. Not inlined, so its buffer stays out of builtinFilter's frame.
//
//go:noinline
func jsonStr(v val, at int) string {
	var buf [128]byte
	return string(appendJSON(buf[:0], v, at))
}

// appendJSON is JSON.stringify for values whose output Go can reproduce: objects bail,
// because JavaScript keeps their key order and Go maps don't.
func appendJSON(dst []byte, v val, at int) []byte {
	if s, ok := v.str(); ok {
		return appendJSONString(dst, s)
	}
	if f, ok := v.check(at).num(at); ok {
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return append(dst, "null"...) // JSON.stringify's form
		}
		return appendJSNumber(dst, f)
	}
	switch x := v.x.(type) {
	case nil:
		return append(dst, "null"...)
	case bool:
		return strconv.AppendBool(dst, x)
	case []any:
		dst = append(dst, '[')
		for i, e := range x {
			if i > 0 {
				dst = append(dst, ',')
			}
			if e == nil {
				dst = append(dst, "null"...) // JSON.stringify writes null for undefined elements too
				continue
			}
			dst = appendJSON(dst, val{x: e}, at)
		}
		return append(dst, ']')
	}
	bail(at, "json-type", "json of %T", v.any())
	return nil
}

func appendJSONString(dst []byte, s string) []byte {
	const hex = "0123456789abcdef"
	dst = append(dst, '"')
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '"', '\\':
			dst = append(dst, '\\', c)
		case '\b':
			dst = append(dst, `\b`...)
		case '\f':
			dst = append(dst, `\f`...)
		case '\n':
			dst = append(dst, `\n`...)
		case '\r':
			dst = append(dst, `\r`...)
		case '\t':
			dst = append(dst, `\t`...)
		default:
			if c < 0x20 {
				dst = append(dst, '\\', 'u', '0', '0', hex[c>>4], hex[c&15])
			} else {
				dst = append(dst, c)
			}
		}
	}
	return append(dst, '"')
}
