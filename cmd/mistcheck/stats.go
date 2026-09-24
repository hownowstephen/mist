package main

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"

	"github.com/hownowstephen/mist"
)

var (
	filterName = regexp.MustCompile(`^\s*([\w-]+)`)
	digits     = regexp.MustCompile(`\d+`)
	quoted     = regexp.MustCompile(`"[^"]*"`)
)

// blockers lists every unsupported construct in tpl, deduplicated. It repeatedly
// checks tpl and replaces the token each error points at with a supported token of
// the same structural role, so later constructs surface without cascading errors.
// ponytail: re-checks from the start per blocker, O(n·blockers); fine for a CLI.
func blockers(tpl string) []string {
	var kinds []string
	add := func(k string) {
		if !slices.Contains(kinds, k) {
			kinds = append(kinds, k)
		}
	}
	unknown := map[string]bool{} // unsupported tags, whose end tags aren't blockers of their own
	for range 1000 {
		e, ok := errors.AsType[*mist.Error](mist.Check(tpl))
		if !ok {
			break
		}
		start, end, isTag := token(tpl, e.Pos)
		if start < 0 {
			if len(kinds) == 0 {
				add("structure")
			}
			break
		}
		repl, ks := classify(tpl[start:end], isTag, e.Msg, unknown)
		if len(ks) == 0 && len(kinds) == 0 {
			ks = []string{"structure"}
		}
		for _, k := range ks {
			add(k)
		}
		if repl == tpl[start:end] {
			repl = ""
		}
		tpl = tpl[:start] + repl + tpl[end:]
	}
	return kinds
}

// token finds the {{ }} or {% %} containing pos.
func token(tpl string, pos int) (start, end int, isTag bool) {
	upto := tpl[:min(len(tpl), pos+2)]
	o, t := strings.LastIndex(upto, "{{"), strings.LastIndex(upto, "{%")
	start, isTag, closer := o, false, "}}"
	if t > o {
		start, isTag, closer = t, true, "%}"
	}
	if start < 0 {
		return -1, -1, false
	}
	k := strings.Index(tpl[start+2:], closer)
	if k < 0 || start+2+k+2 <= pos {
		return -1, -1, false
	}
	return start, start + 2 + k + 2, isTag
}

// classify names what makes a token unsupported and returns a supported stand-in.
func classify(tok string, isTag bool, msg string, unknown map[string]bool) (string, []string) {
	inner := strings.Trim(tok[2:len(tok)-2], "-")
	fields := strings.Fields(inner)
	name := ""
	if isTag && len(fields) > 0 {
		name = fields[0]
	}

	switch {
	case strings.HasPrefix(msg, "unsupported tag"):
		if strings.HasPrefix(name, "end") && unknown[name[3:]] {
			return "", nil
		}
		unknown[name] = true
		return "", []string{"tag:" + name}
	case strings.HasPrefix(msg, "unexpected else"), strings.HasPrefix(msg, "unexpected elsif"),
		strings.HasPrefix(msg, "unexpected end"), msg == "unclosed block":
		return "", nil // left behind by a replaced tag, or counted as structure by blockers
	}

	var kinds []string
	code := quoted.ReplaceAllString(strings.ReplaceAll(inner, "'", `"`), `""`)
	if segs := strings.Split(code, "|"); len(segs) > 1 {
		for _, s := range segs[1:] {
			if m := filterName.FindStringSubmatch(s); m != nil {
				kinds = append(kinds, "filter:"+m[1])
			}
		}
	}
	words := strings.FieldsFunc(code, func(r rune) bool {
		return !(r == '_' || r == '.' || r == '-' || 'a' <= r && r <= 'z' || 'A' <= r && r <= 'Z' || '0' <= r && r <= '9')
	})
	for _, w := range words {
		switch {
		case strings.HasPrefix(w, "forloop"):
			kinds = append(kinds, "forloop")
		case w == "contains", w == "empty":
			kinds = append(kinds, w)
		}
	}
	if name == "for" {
		if strings.Contains(code, "(") {
			kinds = append(kinds, "for:range")
		}
		for _, p := range []string{"limit", "offset", "reversed"} {
			if slices.Contains(words, p) || strings.Contains(code, p+":") {
				kinds = append(kinds, "for:"+p)
			}
		}
	}
	if len(kinds) == 0 {
		kinds = append(kinds, msgKind(msg))
	}

	switch name {
	case "if", "elsif", "unless":
		return "{%" + name + " 1%}", kinds
	case "for":
		return "{%for a in a%}", kinds
	case "else", "endif", "endunless", "endfor", "comment", "endcomment", "raw", "endraw":
		return "{%" + name + "%}", kinds
	}
	return "", kinds
}

func msgKind(msg string) string {
	switch {
	case msg == "mixed and/or":
		return "mixed and/or"
	case strings.Contains(msg, "escape"):
		return "string escapes"
	case strings.HasPrefix(msg, "only integer literals"):
		return "float literal"
	case strings.HasPrefix(msg, "blank"):
		return "blank outside ==/!="
	case strings.Contains(msg, "is not supported as a variable"):
		return "reserved word as variable"
	case strings.HasPrefix(msg, "trim markers on raw"):
		return "raw with trim markers"
	}
	return "other: " + digits.ReplaceAllString(quoted.ReplaceAllString(msg, `"…"`), "N")
}

type tally struct{ in, sole int }

// report prints how many templates each blocker appears in, and how many it alone blocks.
func report(w io.Writer, templates [][]string) {
	counts := map[string]*tally{}
	clean := 0
	for _, ks := range templates {
		if len(ks) == 0 {
			clean++
		}
		for _, k := range ks {
			if counts[k] == nil {
				counts[k] = &tally{}
			}
			counts[k].in++
			if len(ks) == 1 {
				counts[k].sole++
			}
		}
	}
	n := len(templates)
	fmt.Fprintf(w, "%d templates, %d in spec (%.1f%%)\n", n, clean, 100*float64(clean)/float64(max(n, 1)))
	if len(counts) == 0 {
		return
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b string) int {
		return cmp.Or(cmp.Compare(counts[b].sole, counts[a].sole), cmp.Compare(counts[b].in, counts[a].in), cmp.Compare(a, b))
	})
	fmt.Fprintf(w, "\n%-32s %10s %12s\n", "blocker", "templates", "only blocker")
	for _, k := range keys {
		fmt.Fprintf(w, "%-32s %10d %12d\n", k, counts[k].in, counts[k].sole)
	}
}
