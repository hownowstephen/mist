package mist

import (
	"unicode/utf8"
)

// Which characters count as whitespace depends on where, as in liquidjs:
//
//	isBlank      ASCII whitespace: around endraw (skipBlank), and with U+00A0
//	             between expression tokens (renderer.ws)
//	isTrimBlank  liquidjs's BLANK class, ASCII plus U+00A0, U+1680, U+180E, U+2000–200A,
//	             U+2028, U+2029, U+202F, U+205F, U+3000: what {%- and -%} trim
//	jsSpace      JavaScript's \s, BLANK without U+180E but with U+FEFF: String#trim,
//	             split(/\s+/), BlankDrop, and the filters that mirror them

// jsSpace is JavaScript's \s, which BlankDrop tests strings against.
func jsSpace(c rune) bool {
	switch c {
	case ' ', '\t', '\n', '\v', '\f', '\r', 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return c >= 0x2000 && c <= 0x200A
}

func isBlank(c byte) bool { return c == ' ' || (c >= '\t' && c <= '\r') }

func skipBlank(s string, i int) int {
	for i < len(s) && isBlank(s[i]) {
		i++
	}
	return i
}

// isTrimBlank matches liquidjs's BLANK character class, which whitespace control trims greedily.
func isTrimBlank(c rune) bool {
	switch c {
	case ' ', '\t', '\n', '\v', '\f', '\r',
		0xA0, 0x1680, 0x180E, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000:
		return true
	}
	return c >= 0x2000 && c <= 0x200A
}

func trimLeftBlank(s string) string {
	for len(s) > 0 {
		c, n := utf8.DecodeRuneInString(s)
		if !isTrimBlank(c) {
			break
		}
		s = s[n:]
	}
	return s
}

func trimRightBlank(s string) string {
	for len(s) > 0 {
		c, n := utf8.DecodeLastRuneInString(s)
		if !isTrimBlank(c) {
			break
		}
		s = s[:len(s)-n]
	}
	return s
}
