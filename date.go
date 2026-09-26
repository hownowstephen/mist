package mist

import (
	"math"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// defaultDateFormat is liquidjs's dateFormat option default.
const defaultDateFormat = "%A, %B %-e, %Y at %-l:%M %P %z"

const maxMs = 8.64e15 // JavaScript's Date range

var zones sync.Map // name → *time.Location

var (
	dayNames   = [...]string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}
	monthNames = [...]string{"January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"}
)

// dateFilter mirrors liquidjs's date filter running with TZ=UTC and the en-US locale.
func (r *renderer) dateFilter(v val, args []val, at int) val {
	if isNil(v) {
		return v
	}
	ms, ok := r.dateInput(v, at)
	if !ok {
		return v // liquidjs returns what it can't parse unchanged
	}
	format := defaultDateFormat
	if len(args) > 0 && !isNil(args[0]) {
		format = string(stringify(nil, args[0], at))
	}
	z := zone{}
	if len(args) > 1 && !isNil(args[1]) {
		z = tzArg(args[1], ms, at)
	}
	return val{s: string(strftime(nil, ms, z, format, at)), lit: litStr}
}

// dateInput returns v as epoch milliseconds, or false where liquidjs's parseDate fails.
func (r *renderer) dateInput(v val, at int) (int64, bool) {
	s, isStr := v.str()
	switch {
	case !isStr:
		f, ok := v.check(at).num(at)
		if !ok {
			bail(at, "date of %T", v.any())
		}
		return clipMs(f * 1000)
	case s == "now" || s == "today":
		now := time.Now
		if r.now != nil {
			now = r.now
		}
		return now().UnixMilli(), true
	case s == "":
		return 0, false
	case strings.Trim(s, "0123456789") == "":
		f, _ := strconv.ParseFloat(s, 64)
		return clipMs(f * 1000)
	}
	t, ok := isoMs(s)
	if !ok {
		// ponytail: V8's legacy parser accepts far more; widen this if real inputs need it.
		bail(at, "date of a string outside the ISO 8601 subset")
	}
	return t, true
}

// isoMs parses YYYY-MM-DD[(T| )HH:MM[:SS[.fraction]][Z|±HH:MM]] with in-range fields.
// V8 rolls over or rejects out-of-range fields; neither is worth mirroring.
func isoMs(s string) (int64, bool) {
	if len(s) < 10 || s[4] != '-' || s[7] != '-' {
		return 0, false
	}
	y, ok1 := digits(s[:4])
	mo, ok2 := digits(s[5:7])
	d, ok3 := digits(s[8:10])
	var h, mi, sec, ms, off int
	ok4, ok5, ok6 := true, true, true
	rest := s[10:]
	if rest != "" {
		if len(rest) < 6 || (rest[0] != 'T' && rest[0] != ' ') || rest[3] != ':' {
			return 0, false
		}
		h, ok4 = digits(rest[1:3])
		mi, ok5 = digits(rest[4:6])
		rest = rest[6:]
		if len(rest) >= 3 && rest[0] == ':' {
			sec, ok6 = digits(rest[1:3])
			rest = rest[3:]
			if rest != "" && rest[0] == '.' {
				n := 1
				for n < len(rest) && rest[n] >= '0' && rest[n] <= '9' {
					n++
				}
				if n == 1 || n > 10 {
					return 0, false
				}
				ms, _ = digits((rest[1:n] + "00")[:3]) // V8 truncates to milliseconds
				rest = rest[n:]
			}
		}
		switch {
		case rest == "" || rest == "Z":
		case len(rest) == 6 && (rest[0] == '+' || rest[0] == '-') && rest[3] == ':':
			oh, ok7 := digits(rest[1:3])
			om, ok8 := digits(rest[4:6])
			if !ok7 || !ok8 || oh > 23 || om > 59 {
				return 0, false
			}
			off = oh*60 + om
			if rest[0] == '-' {
				off = -off
			}
		default:
			return 0, false
		}
	}
	if !ok1 || !ok2 || !ok3 || !ok4 || !ok5 || !ok6 || mo < 1 || mo > 12 || d < 1 ||
		d > time.Date(y, time.Month(mo)+1, 0, 0, 0, 0, 0, time.UTC).Day() || h > 23 || mi > 59 || sec > 59 {
		return 0, false
	}
	return time.Date(y, time.Month(mo), d, h, mi, sec, ms*1e6, time.UTC).UnixMilli() - int64(off)*60000, true
}

// digits parses a non-empty run of ASCII digits.
func digits(s string) (int, bool) {
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
		n = n*10 + int(s[i]-'0')
	}
	return n, s != ""
}

// clipMs is JavaScript's TimeClip: false for an invalid date.
func clipMs(f float64) (int64, bool) {
	if math.IsNaN(f) || math.Abs(f) > maxMs {
		return 0, false
	}
	return int64(f), true
}

// zone is the date filter's timezone argument: an offset east of UTC in minutes,
// and the name %Z prints. fixed is false without an argument.
type zone struct {
	east  int
	name  string
	fixed bool
}

func tzArg(v val, ms int64, at int) zone {
	if s, ok := v.str(); ok {
		if !ianaName(s) {
			bail(at, "date timezone %q", s)
		}
		loc, ok := zones.Load(s)
		if !ok {
			l, err := time.LoadLocation(s)
			if err != nil {
				bail(at, "date timezone %q: %v", s, err) // Intl may still know it; liquidjs throws if not
			}
			loc, _ = zones.LoadOrStore(s, l)
		}
		_, secs := time.UnixMilli(ms).In(loc.(*time.Location)).Zone()
		if secs%60 != 0 {
			bail(at, "date timezone %q with a sub-minute offset", s)
		}
		return zone{east: secs / 60, name: s, fixed: true}
	}
	f, ok := v.check(at).num(at)
	if !ok || f != math.Trunc(f) || math.Abs(f) > 1e6 {
		bail(at, "date timezone argument %v", v.any())
	}
	return zone{east: -int(f), fixed: true} // minutes west, as in getTimezoneOffset
}

// ianaName rejects what Go's LoadLocation would load but Intl rejects: Local, Factory,
// and zoneinfo's lower-case and dotted support files.
func ianaName(s string) bool {
	if s == "" || s[0] < 'A' || s[0] > 'Z' || s == "Local" || s == "Factory" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', strings.IndexByte("_+-/", c) >= 0:
		default:
			return false
		}
	}
	return true
}

// strftime appends liquidjs's strftime of the instant ms displayed in z.
func strftime(dst []byte, ms int64, z zone, format string, at int) []byte {
	disp := ms + int64(z.east)*60000
	d := time.UnixMilli(disp).UTC()
	if y := d.Year(); y < 1000 || y > 9999 {
		bail(at, "date outside years 1000–9999") // liquidjs's %y and %C assume four digits
	}
	for i := 0; i < len(format); {
		j := strings.IndexByte(format[i:], '%')
		if j < 0 {
			return append(dst, format[i:]...)
		}
		dst = append(dst, format[i:i+j]...)
		i += j
		// A directive is %[-_0^#:]*[0-9]*[EO]? then any character but a newline.
		k := i + 1
		for k < len(format) && strings.IndexByte("-_0^#:", format[k]) >= 0 {
			k++
		}
		flags := format[i+1 : k]
		w := k
		for k < len(format) && format[k] >= '0' && format[k] <= '9' {
			k++
		}
		width := format[w:k]
		if k < len(format) && (format[k] == 'E' || format[k] == 'O') {
			k++
		}
		if k == len(format) || format[k] == '\n' {
			// liquidjs's regex backtracks onto a flag, digit or modifier, none of which
			// converts, so the text prints as written.
			dst = append(dst, '%')
			i++
			continue
		}
		_, size := utf8.DecodeRuneInString(format[k:])
		conv := format[k : k+size]
		k += size
		ret, ok := strftimeConv(d, disp, z, conv, flags, width, at)
		if !ok {
			dst = append(dst, format[i:k]...)
			i = k
			continue
		}
		i = k
		pad := byte('0')
		if strings.Contains("aAbBceklpP", conv) {
			pad = ' '
		}
		padWidth := 0
		switch {
		case width != "":
			padWidth = strWidth(width, at)
		case strings.Contains("deHIklmMSUW", conv):
			padWidth = 2
		case conv == "j" || conv == "L":
			padWidth = 3
		}
		if strings.Contains(flags, "^") {
			ret = strings.ToUpper(ret)
		} else if strings.Contains(flags, "#") {
			if strings.ContainsFunc(ret, func(c rune) bool { return c >= 'a' && c <= 'z' }) {
				ret = strings.ToUpper(ret)
			} else {
				ret = strings.ToLower(ret)
			}
		}
		if strings.Contains(flags, "_") {
			pad = ' '
		} else if strings.Contains(flags, "0") {
			pad = '0'
		}
		if strings.Contains(flags, "-") {
			padWidth = 0
		}
		for range padWidth - len(ret) {
			dst = append(dst, pad)
		}
		dst = append(dst, ret...)
	}
	return dst
}

func strWidth(w string, at int) int {
	n, err := strconv.Atoi(w)
	if err != nil || n > 1024 {
		bail(at, "date format width %s", w)
	}
	return n
}

// strftimeConv is one of liquidjs's formatCodes; false means print the directive as is.
func strftimeConv(d time.Time, disp int64, z zone, conv, flags, width string, at int) (string, bool) {
	itoa := strconv.Itoa
	h12 := d.Hour() % 12
	if h12 == 0 {
		h12 = 12
	}
	switch conv {
	case "a":
		return dayNames[d.Weekday()][:3], true
	case "A":
		return dayNames[d.Weekday()], true
	case "b", "h":
		return monthNames[d.Month()-1][:3], true
	case "B":
		return monthNames[d.Month()-1], true
	case "c", "x", "X":
		bail(at, "date %%%s, which prints with the ICU locale", conv)
	case "C":
		return itoa(d.Year() / 100), true
	case "d", "e":
		return itoa(d.Day()), true
	case "H", "k":
		return itoa(d.Hour()), true
	case "I", "l":
		return itoa(h12), true
	case "j":
		return itoa(d.YearDay()), true
	case "L":
		return itoa(d.Nanosecond() / 1e6), true
	case "m":
		return itoa(int(d.Month())), true
	case "M":
		return itoa(d.Minute()), true
	case "N":
		w := 9
		if width != "" {
			w = strWidth(width, at) // never 0: flags take a leading 0
		}
		s := itoa(d.Nanosecond() / 1e6)
		if len(s) > w {
			s = s[:w]
		}
		return s + strings.Repeat("0", w-len(s)), true
	case "p":
		if d.Hour() < 12 {
			return "AM", true
		}
		return "PM", true
	case "P":
		if d.Hour() < 12 {
			return "am", true
		}
		return "pm", true
	case "q":
		switch day := d.Day(); {
		case day >= 11 && day <= 13:
			return "th", true
		case day%10 == 1:
			return "st", true
		case day%10 == 2:
			return "nd", true
		case day%10 == 3:
			return "rd", true
		}
		return "th", true
	case "s":
		return strconv.FormatInt(int64(math.Floor(float64(disp+500)/1000)), 10), true // Math.round(ms / 1000)
	case "S":
		return itoa(d.Second()), true
	case "u":
		if d.Weekday() == 0 {
			return "7", true
		}
		return itoa(int(d.Weekday())), true
	case "U":
		return itoa(weekOfYear(d, 0)), true
	case "w":
		return itoa(int(d.Weekday())), true
	case "W":
		return itoa(weekOfYear(d, 1)), true
	case "y":
		return itoa(d.Year())[2:4], true
	case "Y":
		return itoa(d.Year()), true
	case "z":
		return tzOffset(z.east, strings.Contains(flags, ":")), true
	case "Z":
		if !z.fixed {
			bail(at, "date %%Z without a timezone, which prints the process's zone name")
		}
		if z.name != "" {
			return z.name, true
		}
		return tzOffset(z.east, strings.Contains(flags, ":")), true
	case "t":
		return "\t", true
	case "n":
		return "\n", true
	case "%":
		return "%", true
	}
	return "", false
}

func tzOffset(east int, colon bool) string {
	sign := "+"
	if east < 0 {
		sign, east = "-", -east
	}
	sep := ""
	if colon {
		sep = ":"
	}
	return sign + pad2(east/60) + sep + pad2(east%60)
}

func pad2(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

// weekOfYear is liquidjs's getWeekOfYear: weeks starting on startDay (0 Sunday, 1 Monday).
func weekOfYear(d time.Time, startDay int) int {
	now := d.YearDay() + startDay - int(d.Weekday())
	jan1 := time.Date(d.Year(), 1, 1, 0, 0, 0, 0, time.UTC).Weekday()
	then := 7 - int(jan1) + startDay
	return int(math.Floor(float64(now-then)/7)) + 1
}
