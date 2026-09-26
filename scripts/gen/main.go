// Command gen writes random templates drawn from the SPEC.md grammar, with random
// data, in harvest.rb's format for oracle.mjs. Deterministic for a given seed.
//
//	go run ./scripts/gen -n 5000 -seed 1 > /tmp/generated.json
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math/rand/v2"
	"os"
	"strings"
)

var (
	names  = []string{"a", "b", "c", "x", "xs", "n", "s", "user", "items", "size", "first", "f-g", "f", "tz"}
	props  = []string{"a", "b", "name", "id", "k", "size", "first", "last", "nested"}
	words  = []string{"", " ", "hi", "  \n ", "\t", " ", "é", "{", "}", "%", "<p>"}
	blanks = []string{"", " ", "  ", "\n", " \t "}
	dates  = []string{"'%Y-%m-%d'", "'%s'", "'%H:%M:%S.%L %N'", "'%a %b %e %-l%P %z'", "'%A %B %-d, %Y %I:%M %p'", "'%j %U %W %u %w %y %C %q'", "'%:z %^a %#B %_5d %-m%%'", "'%Q %'", "f"}
	zones  = []string{"'UTC'", "'America/New_York'", "'Asia/Kolkata'", "'Australia/Adelaide'", "'US/Pacific'", "-330", "300", "tz"}
)

type gen struct {
	r     *rand.Rand
	loops []string
	b     strings.Builder
}

func (g *gen) pick(xs []string) string    { return xs[g.r.IntN(len(xs))] }
func (g *gen) ws() string                 { return g.pick(blanks) }
func (g *gen) pickF(xs []float64) float64 { return xs[g.r.IntN(len(xs))] }

func (g *gen) path() string {
	var p string
	if len(g.loops) > 0 && g.r.IntN(2) == 0 {
		p = g.pick(g.loops)
	} else {
		p = g.pick(names)
	}
	for range g.r.IntN(3) {
		switch g.r.IntN(4) {
		case 0:
			p += fmt.Sprintf("[%d]", g.r.IntN(5)-2)
		case 1:
			p += fmt.Sprintf("[%q]", g.pick(props))
		default:
			p += "." + g.pick(props)
		}
	}
	return p
}

func (g *gen) expr() string {
	switch g.r.IntN(8) {
	case 0:
		return fmt.Sprintf("'%s'", g.pick([]string{"", "a", "b", "A", "10", "2", "now"}))
	case 1:
		return fmt.Sprint(g.r.IntN(21) - 10)
	case 2:
		return g.pick([]string{"true", "false", "nil", "null"})
	}
	return g.path()
}

// filters returns an optional chain of the supported built-in filters.
func (g *gen) filters() string {
	var f string
	for range g.r.IntN(3) {
		switch g.r.IntN(9) {
		case 6, 7:
			f += " | " + g.pick([]string{"downcase", "upcase", "strip", "lstrip", "rstrip", "escape", "escape_once", "url_encode", "json", "first", "last"})
		case 8:
			f += " | " + g.pick([]string{"append", "prepend", "replace", "replace_first", "remove", "remove_first", "strip", "truncate", "truncatewords", "split", "slice", "plus", "minus", "times", "divided_by", "modulo"}) + ": " + g.expr()
			if g.r.IntN(3) == 0 {
				f += ", " + g.expr()
			}
		case 0:
			f += " | default"
		case 5:
			f += " | date"
			if g.r.IntN(4) > 0 {
				f += ": " + g.pick(dates)
				if g.r.IntN(2) == 0 {
					f += ", " + g.pick(zones)
				}
			}
		case 1:
			f += " | default: " + g.expr() + g.pick([]string{"", ", allow_false: true", ", allow_false: false"})
		case 2, 3:
			f += " | default: " + g.expr()
		default:
			f += " | capitalize"
		}
	}
	return f
}

func (g *gen) cond() string {
	c := g.cmp()
	join := g.pick([]string{"and", "or"})
	for range g.r.IntN(3) {
		c += " " + join + " " + g.cmp()
	}
	return c
}

func (g *gen) cmp() string {
	if g.r.IntN(2) == 0 {
		return g.expr()
	}
	op := g.pick([]string{"==", "!=", "<", ">", "<=", ">="})
	if (op == "==" || op == "!=") && g.r.IntN(4) == 0 {
		return g.expr() + g.ws() + op + g.ws() + "blank"
	}
	return g.expr() + g.ws() + op + g.ws() + g.expr()
}

func (g *gen) open(tag string) string {
	l, r := g.pick([]string{"{%", "{%-"}), g.pick([]string{"%}", "-%}"})
	return l + " " + g.ws() + tag + g.ws() + " " + r
}

func (g *gen) block(depth int) {
	for range g.r.IntN(4) + 1 {
		switch k := g.r.IntN(10); {
		case k < 3:
			g.b.WriteString(g.pick(words))
		case k < 5:
			l, r := g.pick([]string{"{{", "{{-"}), g.pick([]string{"}}", "-}}"})
			g.b.WriteString(l + g.ws() + g.expr() + g.filters() + g.ws() + r)
		case k == 5 && depth < 4:
			tag := g.pick([]string{"if", "unless"})
			g.b.WriteString(g.open(tag + " " + g.cond()))
			g.block(depth + 1)
			for range g.r.IntN(3) {
				g.b.WriteString(g.open("elsif " + g.cond()))
				g.block(depth + 1)
			}
			if g.r.IntN(2) == 0 {
				g.b.WriteString(g.open("else"))
				g.block(depth + 1)
			}
			g.b.WriteString(g.open("end" + tag))
		case k == 6 && depth < 4:
			v := g.pick([]string{"i", "item", "x"})
			g.b.WriteString(g.open("for " + v + " in " + g.path()))
			g.loops = append(g.loops, v)
			g.block(depth + 1)
			g.loops = g.loops[:len(g.loops)-1]
			g.b.WriteString(g.open("endfor"))
		case k == 7:
			g.b.WriteString(g.open("assign " + g.pick(names[:6]) + g.ws() + "=" + g.ws() + g.expr() + g.filters()))
		case k == 8:
			g.b.WriteString("{% comment %}" + g.pick(words) + "{{ x }}{% endcomment %}")
		default:
			g.b.WriteString("{% raw %}{{ " + g.pick(words) + " }}{% endraw %}")
		}
	}
}

func (g *gen) value(depth int) any {
	switch g.r.IntN(10) {
	case 0:
		return nil
	case 1:
		return g.r.IntN(2) == 0
	case 2:
		return float64(g.r.IntN(21) - 10)
	case 3:
		return g.pickF([]float64{0.5, -1.25, 99.99, 0.1 + 0.2, 1e21, 1e-7, 2.5e-8, 123456789012345680000, 1700000000, 1710460800.5, -86400})
	case 4:
		return g.pick([]string{"", "a", "b", "A", "10", "2", " x ", "  ", "\u00a0", "\u0085", "hELLO wORLD", "élodie", "иВАН", "ßtraße", "ΣΟΦΙΑΣ",
			"now", "1700000000", "2024-02-29", "2024-03-10T07:30:00Z", "2024-11-03 01:30:00", "2024-03-15T10:20:30.5+05:45", "2024-02-30", "Mar 5 2024", "%d/%m", "Europe/Paris",
			"a,b,,c", "one two  three", "a&amp;b<c>", "😀 hi", " 12 ", "0x1f", "Hello World, and more words than fit"})
	case 5, 6:
		if depth < 3 {
			m := map[string]any{}
			for range g.r.IntN(4) {
				m[g.pick(props)] = g.value(depth + 1)
			}
			return m
		}
	case 7, 8:
		if depth < 3 {
			var a []any
			for range g.r.IntN(4) {
				a = append(a, g.value(depth+1))
			}
			return a
		}
	}
	return 1.0
}

func main() {
	n := flag.Int("n", 5000, "number of cases")
	seed := flag.Uint64("seed", 1, "random seed")
	flag.Parse()
	g := &gen{r: rand.New(rand.NewPCG(*seed, 0))}
	var cases []map[string]any
	for i := range *n {
		g.b.Reset()
		g.block(0)
		data := map[string]any{}
		for _, k := range names {
			if g.r.IntN(3) > 0 {
				data[k] = g.value(0)
			}
		}
		cases = append(cases, map[string]any{"src": "generated", "name": fmt.Sprintf("gen-%d-%d", *seed, i), "tpl": g.b.String(), "data": data})
	}
	if err := json.NewEncoder(os.Stdout).Encode(cases); err != nil {
		panic(err)
	}
}
