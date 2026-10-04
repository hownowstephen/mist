package mist

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

type tcase struct {
	Name   string         `json:"name"`
	Tpl    string         `json:"tpl"`
	Data   map[string]any `json:"data"`
	Strict bool           `json:"strict"`
	Out    string         `json:"out"`
	Err    string         `json:"err"`
	Now    *int64         `json:"now"` // Date.now in ms, for 'now' and 'today'
}

func loadCases(t testing.TB) []tcase {
	b, err := os.ReadFile("testdata/cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cs []tcase
	if err := json.Unmarshal(b, &cs); err != nil {
		t.Fatal(err)
	}
	return cs
}

func TestCases(t *testing.T) {
	for _, c := range loadCases(t) {
		t.Run(c.Name, func(t *testing.T) {
			var e Engine
			if c.Now != nil {
				e.Now = func() time.Time { return time.UnixMilli(*c.Now) }
			}
			out, err := e.Render(c.Tpl, c.Data, c.Strict)
			switch c.Err {
			case "":
				if err != nil || out != c.Out {
					t.Fatalf("got %q, %v; want %q", out, err, c.Out)
				}
			case "undefined":
				if !errors.Is(err, ErrUndefined) {
					t.Fatalf("got %q, %v; want ErrUndefined", out, err)
				}
			case "unsupported":
				if !errors.Is(err, ErrUnsupported) {
					t.Fatalf("got %q, %v; want ErrUnsupported", out, err)
				}
			}
			// Everything that renders or fails authoritatively is in spec.
			if c.Err == "" {
				if err := Check(c.Tpl); err != nil {
					t.Fatalf("Check: %v", err)
				}
			}
		})
	}
}

func TestChain(t *testing.T) {
	vars := map[string]any{"customer": map[string]any{"name": "Ada"}, "snippets": map[string]any{}}
	steps := []Step{
		{Body: "Hi {{ customer.name }}", Key: []string{"snippets", "greeting"}},
		{Body: "{{ snippets.greeting }}, welcome", Key: []string{"message", "subject"}, Strict: true},
		{Body: "<p>{{ message.subject }}</p>", Key: []string{"content"}},
		{Body: "{{ content }}|{{ missing }}", Strict: true},
		{Body: "<html>{{ content }}</html>"},
	}
	res, n, got := RenderChain(steps, vars)
	if n != 5 {
		t.Fatalf("n = %d, want 5", n)
	}
	want := []string{"Hi Ada", "Hi Ada, welcome", "<p>Hi Ada, welcome</p>"}
	for i, w := range want {
		if res[i].Err != nil || res[i].Out != w {
			t.Fatalf("step %d: got %q, %v; want %q", i, res[i].Out, res[i].Err, w)
		}
	}
	if !errors.Is(res[3].Err, ErrUndefined) {
		t.Fatalf("step 3: want ErrUndefined, got %v", res[3].Err)
	}
	if got["content"] != "<p>Hi Ada, welcome</p>" {
		t.Fatalf("content = %v", got["content"])
	}
	if len(vars["snippets"].(map[string]any)) != 0 || vars["message"] != nil {
		t.Fatal("caller vars were mutated")
	}
}

func TestChainContentOnError(t *testing.T) {
	steps := []Step{
		{Body: "{{ nope }}", Key: []string{"content"}, Strict: true},
		{Body: "[{{ content }}]"},
	}
	res, n, _ := RenderChain(steps, nil)
	if n != 2 || res[1].Out != "[{{ nope }}]" {
		t.Fatalf("got n=%d %+v; want raw body bound as content", n, res)
	}
}

func TestChainBailStopsEarly(t *testing.T) {
	steps := []Step{
		{Body: "a", Key: []string{"x"}},
		{Body: "{{ x | md5 }}", Key: []string{"y"}},
		{Body: "{{ y }}"},
	}
	res, n, vars := RenderChain(steps, map[string]any{})
	if n != 1 || len(res) != 1 || vars["x"] != "a" {
		t.Fatalf("got n=%d res=%+v vars=%v", n, res, vars)
	}
}

func TestUndefinedNamesWholePath(t *testing.T) {
	_, err := Render("{{ trigger.first_name[0] }}", map[string]any{}, true)
	var e *Error
	if !errors.As(err, &e) || !errors.Is(err, ErrUndefined) || e.Msg != "trigger.first_name[0]" {
		t.Fatalf("got %v; want ErrUndefined naming trigger.first_name[0]", err)
	}
}

func TestErrorPosition(t *testing.T) {
	_, err := Render("line1\n{{ a | b }}", nil, false)
	var e *Error
	if !errors.As(err, &e) || e.Pos != 13 {
		t.Fatalf("got %v; want unsupported at offset 13, the filter name", err)
	}
}

func FuzzRender(f *testing.F) {
	for _, c := range loadCases(f) {
		f.Add(c.Tpl)
	}
	if b, err := os.ReadFile("testdata/corpus.json"); err == nil {
		var cs []corpusCase
		if json.Unmarshal(b, &cs) == nil {
			for _, c := range cs {
				f.Add(c.Tpl)
			}
		}
	}
	vars := map[string]any{"a": map[string]any{"b": "x", "n": 2.0}, "xs": []any{1.0, "s", nil}, "t": true, "s": "str"}
	f.Fuzz(func(t *testing.T, tpl string) {
		_, err1 := Render(tpl, vars, false)
		_, err2 := Render(tpl, vars, true)
		d := Engine{Dialect: &Dialect{Reject: TrimMarkers | NegativeLiterals | UnspacedOperators | RawBlocks | BlankKeyword}}
		if err := d.Check(tpl); err != nil {
			if _, rerr := d.Render(tpl, vars, false); rerr == nil {
				t.Fatalf("dialect Check rejected (%v) but Render accepted", err)
			}
		} else if _, rerr := d.Render(tpl, vars, false); errors.Is(rerr, ErrUnsupported) && !runtimeBail(rerr) {
			t.Fatalf("dialect Check accepted but Render bailed on syntax: %v", rerr)
		}
		if checkErr := Check(tpl); checkErr != nil {
			// Check parses a superset of what Render parses, so Render must fail too.
			if err1 == nil || err2 == nil {
				t.Fatalf("Check rejected (%v) but Render accepted", checkErr)
			}
		} else if errors.Is(err1, ErrUnsupported) && !runtimeBail(err1) {
			t.Fatalf("Check accepted but Render bailed on syntax: %v", err1)
		}
	})
}

// runtimeBail reports data-dependent bails, which Check can't see.
func runtimeBail(err error) bool {
	for _, s := range []string{"cannot output", "property", "index on", "for over", "built-in", "needs two", "between number", "== with", "unsupported value", "assigning nil", "stringify of an object", "capitalize of", "date ", "math filter", "filter argument", "slice without", "first or last", "last of", "first of", "number from", "json of", "case mapping", "string filter", "url_encode of", "nil literal as a filter", "contains with an object", "round to", "strip_html of", "join of", "size of", "where ", "index of", "for offset", "for limit", "range ", "map of", "sum of", "sort of", "sort by", "find on", "push of", "reverse of", "array filter on", "loop iterations", "sort_natural", "base64_decode", "url_decode", "uniq of", "invalid UTF-8", "slugify", "array_to_sentence_string of", "unshift of", "group_by of", "string of"} {
		if strings.Contains(err.Error(), s) {
			return true
		}
	}
	return false
}

const benchTpl = `<p>Hi {{ customer.first_name }},</p>
{% if customer.plan == "pro" %}<p>Thanks for being a Pro member since {{ customer.since }}.</p>{% else %}<p>Upgrade today!</p>{% endif %}
<ul>{% for item in event.items %}<li>{{ item.name }} x{{ item.qty }}</li>{% endfor %}</ul>
{% unless customer.unsubscribed %}<a href="https://example.com/u/{{ customer.id }}">Unsubscribe</a>{% endunless %}`

func BenchmarkAppend(b *testing.B) {
	vars := map[string]any{
		"customer": map[string]any{"first_name": "Ada", "plan": "pro", "since": "2019", "id": 42.0, "unsubscribed": false},
		"event":    map[string]any{"items": []any{map[string]any{"name": "Widget", "qty": 2.0}, map[string]any{"name": "Gadget", "qty": 1.0}}},
	}
	buf := make([]byte, 0, 1024)
	b.ReportAllocs()
	b.SetBytes(int64(len(benchTpl)))
	for b.Loop() {
		var err error
		if buf, err = Append(buf[:0], benchTpl, vars, true); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRender(b *testing.B) {
	vars := map[string]any{
		"customer": map[string]any{"first_name": "Ada", "plan": "pro", "since": "2019", "id": 42.0, "unsubscribed": false},
		"event":    map[string]any{"items": []any{map[string]any{"name": "Widget", "qty": 2.0}, map[string]any{"name": "Gadget", "qty": 1.0}}},
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(benchTpl)))
	for b.Loop() {
		if _, err := Render(benchTpl, vars, true); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCheck(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if err := Check(benchTpl); err != nil {
			b.Fatal(err)
		}
	}
}

// Go callers can pass values JSON never produces; liquidjs sees their JSON form.
func TestGoValues(t *testing.T) {
	vars := map[string]any{"i": 3, "i64": int64(-4), "jn": json.Number("5.0"), "ss": []string{"a"}, "bad": json.Number("x")}
	for tpl, want := range map[string]string{
		"{{ i }}{{ i64 }}{{ jn }}":                             "3-45",
		"{% if i == 3 and jn == 5 and i64 < 0 %}ok{% endif %}": "ok",
		"{% if i %}t{% endif %}":                               "t",
	} {
		if out, err := Render(tpl, vars, true); err != nil || out != want {
			t.Errorf("%s: got %q, %v; want %q", tpl, out, err, want)
		}
	}
	for _, tpl := range []string{"{{ ss }}", "{% if ss %}{% endif %}", "{% if ss == 1 %}{% endif %}", "{{ bad }}"} {
		if _, err := Render(tpl, vars, false); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s: got %v; want ErrUnsupported", tpl, err)
		}
	}
}

func TestChainKeyThroughScalar(t *testing.T) {
	steps := []Step{{Body: "x", Key: []string{"s", "k"}}, {Body: "y"}}
	res, n, _ := RenderChain(steps, map[string]any{"s": "scalar"})
	if n != 0 || len(res) != 0 {
		t.Fatalf("got n=%d res=%+v; want the step handed to the full engine", n, res)
	}
}

const filterTpl = `<p>Hi {{ customer.first_name | default: "there" | capitalize }},</p>
{% assign plan = customer.plan | default: "free" %}<p>Plan: {{ plan | capitalize }} since {{ customer.since | default: "today" }}.</p>
{% for item in event.items %}<li>{{ item.name | capitalize }} x{{ item.qty | default: 1 }}</li>{% endfor %}`

func BenchmarkFilters(b *testing.B) {
	vars := map[string]any{
		"customer": map[string]any{"first_name": "ada", "plan": "pro"},
		"event":    map[string]any{"items": []any{map[string]any{"name": "widget", "qty": 2.0}, map[string]any{"name": "gadget"}}},
	}
	buf := make([]byte, 0, 1024)
	b.ReportAllocs()
	b.SetBytes(int64(len(filterTpl)))
	for b.Loop() {
		var err error
		if buf, err = Append(buf[:0], filterTpl, vars, true); err != nil {
			b.Fatal(err)
		}
	}
}

// heavyTpl exercises the allocating filters: date with a zone, capture, truncate, escape, url_encode, json.

const heavyTpl = `{% capture greeting %}Hi {{ customer.first_name | strip | capitalize }}{% endcapture %}{{ greeting }}
Today is {{ 'now' | date: '%A, %B %-d, %Y at %l:%M %p %:z', 'Europe/Paris' }} ({{ 'now' | date: '%s' | plus: 86400 | date: '%Y-%m-%d' }}).
{% if customer.tags contains 'vip' %}VIP{% endif %}{% if customer.notes == empty %}none{% endif %}
{{ customer.bio | truncate: 20 }} {{ customer.score | times: 2 | plus: 2 }}
<a href="https://example.com/?q={{ customer.name | url_encode }}">{{ customer.name | escape }}</a>
<script>var c = {{ customer.tags | json }};</script>`

func BenchmarkHeavy(b *testing.B) {
	vars := map[string]any{"customer": map[string]any{
		"first_name": "  ada ", "tags": []any{"a", "vip"}, "notes": "",
		"bio": "A long biography that goes on for a while", "score": 3.0, "name": "Ada & <Co>",
	}}
	buf := make([]byte, 0, 2048)
	b.ReportAllocs()
	for b.Loop() {
		var err error
		if buf, err = Append(buf[:0], heavyTpl, vars, true); err != nil {
			b.Fatal(err)
		}
	}
}

func TestNestedRangesHitTheIterationBudget(t *testing.T) {
	start := time.Now()
	_, err := Render(`{% for i in (1..99999) %}{% for j in (1..99999) %}x{% endfor %}{% endfor %}`, nil, false)
	if !errors.Is(err, ErrUnsupported) || time.Since(start) > 5*time.Second {
		t.Fatalf("got %v after %v; want a quick ErrUnsupported", err, time.Since(start))
	}
}

func TestCaseBailsWithADialectCompareHook(t *testing.T) {
	e := Engine{Dialect: &Dialect{Compare: func(string, any, any) (bool, error) { return true, nil }}}
	if _, err := e.Render(`{% case x %}{% when 1 %}a{% endcase %}`, nil, false); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("got %v; want ErrUnsupported", err)
	}
}

// Go callers can pass numbers that aren't float64; sort orders them numerically.
func TestSortOfGoNumbers(t *testing.T) {
	out, err := Render(`{{ xs | sort | join: ',' }}`, map[string]any{"xs": []any{3, int64(1), json.Number("2")}}, true)
	if err != nil || out != "1,2,3" {
		t.Fatalf("got %q, %v; want 1,2,3", out, err)
	}
}

// modernTpl exercises v0.9–v0.12: case, loops with forloop and limit, ranges, variable
// indexes, filtered and mixed conditions, floats, escapes and the array filters.
const modernTpl = `{% assign total = order.items | sum: 'price' %}{% for item in order.items limit: 4 %}{% unless forloop.first %}, {% endunless %}{{ forloop.index }}. {{ item.name | strip_html | truncate: 20 }}{% case item.kind %}{% when 'gift', 'promo' %} (special){% when 'sale' %} (sale){% endcase %}{% if item.qty > 1 and item.price >= 10.5 or item.flag %} x{{ item.qty }}{% endif %}{% endfor %}
Total: {{ total | times: 1.08 | round: 2 }}
{{ order.items | map: 'name' | sort | join: ", " }}
{% for i in (0..2) %}{{ labels[i] }}{% if forloop.last %}.{% else %}/{% endif %}{% endfor %}
{% assign vip = order.items | where: 'flag' | size %}{% if vip > 0 %}VIP {{ vip }}{% endif %}{% if customer.name | default: false %} {{ "Say \"hi\", " | append: customer.name }}{% endif %}`

var modernVars = map[string]any{
	"customer": map[string]any{"name": "Ada"},
	"labels":   []any{"one", "two", "three"},
	"order": map[string]any{"items": []any{
		map[string]any{"name": "<b>Widget</b>", "kind": "gift", "qty": 2.0, "price": 12.5, "flag": true},
		map[string]any{"name": "Gadget", "kind": "sale", "qty": 1.0, "price": 9.99, "flag": false},
		map[string]any{"name": "Doohickey with a long name", "kind": "plain", "qty": 3.0, "price": 4.25, "flag": false},
		map[string]any{"name": "Thing", "kind": "promo", "qty": 1.0, "price": 20.0, "flag": true},
		map[string]any{"name": "Extra", "kind": "plain", "qty": 5.0, "price": 1.0, "flag": false},
	}},
}

func BenchmarkModern(b *testing.B) {
	buf := make([]byte, 0, 2048)
	b.ReportAllocs()
	for b.Loop() {
		var err error
		if buf, err = Append(buf[:0], modernTpl, modernVars, true); err != nil {
			b.Fatal(err)
		}
	}
}

// uniq keys Go numbers by value, as JavaScript sees them after JSON.
func TestUniqOfGoNumbers(t *testing.T) {
	out, err := Render(`{{ xs | uniq | join: ',' }}`, map[string]any{"xs": []any{1, 1.0, int64(2), json.Number("2"), "2"}}, true)
	if err != nil || out != "1,2,2" {
		t.Fatalf("got %q, %v; want 1,2,2", out, err)
	}
	if _, err := Render(`{{ xs | uniq | size }}`, map[string]any{"xs": []any{json.Number("x")}}, false); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("got %v; want ErrUnsupported", err)
	}
}
