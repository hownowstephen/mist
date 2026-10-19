package mist

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
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
	Code   string         `json:"code"` // Error.Code, required when Err is "unsupported"
	Now    *int64         `json:"now"`  // Date.now in ms, for 'now' and 'today'
	// Mem is what liquidjs charges memoryLimit (parity.mjs checks), and MistMem what mist
	// charges Limits.Memory where that's more: it renders with exactly that much.
	Mem     int `json:"mem"`
	MistMem int `json:"mistMem"`
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
			failInternal(t, err)
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
				var e *Error
				if !errors.Is(err, ErrUnsupported) || !errors.As(err, &e) {
					t.Fatalf("got %q, %v; want ErrUnsupported", out, err)
				}
				if e.Code != c.Code {
					t.Fatalf("got code %q; want %q", e.Code, c.Code)
				}
			}
			if c.Mem > 0 {
				checkMem(t, c)
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

// checkMem checks that c renders with its memory charge but not with one less, and that
// mist charges no less than liquidjs, so it bails wherever liquidjs's memoryLimit would.
func checkMem(t *testing.T, c tcase) {
	t.Helper()
	m := c.Mem
	if c.MistMem != 0 {
		if c.MistMem <= c.Mem {
			t.Fatalf("mistMem %d must be more than mem %d", c.MistMem, c.Mem)
		}
		m = c.MistMem
	}
	e := Engine{Limits: Limits{Memory: m}}
	if out, err := e.Render(c.Tpl, c.Data, c.Strict); err != nil || out != c.Out {
		t.Fatalf("Limits.Memory %d: got %q, %v; want %q", m, out, err, c.Out)
	}
	e.Limits.Memory = m - 1
	var me *Error
	if _, err := e.Render(c.Tpl, c.Data, c.Strict); !errors.As(err, &me) || me.Code != "memory-limit" {
		t.Fatalf("Limits.Memory %d: got %v; want memory-limit", m-1, err)
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
		for _, err := range []error{err1, err2, Check(tpl)} {
			failInternal(t, err)
		}
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

// FuzzInvariants checks properties that need no oracle, on fuzzed data as well as
// templates: rendering is deterministic, Append and a one-step RenderChain agree with
// Render, raising Limits or passing unknown filters never changes what renders, and the
// caller's vars are never mutated. Strict and lax output may differ, as liquidjs's
// lenientIf makes them.
func FuzzInvariants(f *testing.F) {
	for _, c := range loadCases(f) {
		b, _ := json.Marshal(c.Data)
		f.Add(c.Tpl, b, c.Strict)
	}
	if b, err := os.ReadFile("testdata/corpus.json"); err == nil {
		var cs []corpusCase
		if json.Unmarshal(b, &cs) == nil {
			for _, c := range cs {
				b, _ := json.Marshal(c.Data)
				f.Add(c.Tpl, b, false)
			}
		}
	}
	now := func() time.Time { return time.UnixMilli(1700000000123) }
	e := Engine{Now: now}
	raised := Engine{Now: now, PassUnknownFilters: true, Limits: Limits{Memory: 1 << 40, Output: 1 << 30, Iterations: 1 << 30}}
	f.Fuzz(func(t *testing.T, tpl string, data []byte, strict bool) {
		newVars := func() map[string]any {
			var m map[string]any
			if json.Unmarshal(data, &m) != nil {
				m = map[string]any{"a": map[string]any{"b": "x", "n": 2.0}, "xs": []any{1.0, "s", nil}, "t": true, "s": "str"}
			}
			return m
		}
		vars := newVars()
		errStr := func(err error) string {
			if err == nil {
				return ""
			}
			return err.Error()
		}
		out, err := e.Render(tpl, vars, strict)
		failInternal(t, err)
		if out2, err2 := e.Render(tpl, vars, strict); out2 != out || errStr(err2) != errStr(err) {
			t.Fatalf("not deterministic: %q, %v then %q, %v", out, err, out2, err2)
		}
		prefix := []byte("pre\x00")
		app, aerr := e.Append(append(make([]byte, 0, 64), prefix...), tpl, vars, strict)
		switch {
		case errStr(aerr) != errStr(err):
			t.Fatalf("Append: %v; Render: %v", aerr, err)
		case err != nil && app != nil:
			t.Fatalf("Append returned %q with %v", app, err)
		case err == nil && string(app) != string(prefix)+out:
			t.Fatalf("Append: %q; Render with the prefix: %q", app, string(prefix)+out)
		}
		res, n, _ := e.RenderChain([]Step{{Body: tpl, Strict: strict}}, vars)
		switch {
		case errors.Is(err, ErrUnsupported):
			if n != 0 || len(res) != 0 {
				t.Fatalf("RenderChain: step %d, %v; Render bailed: %v", n, res, err)
			}
		case n != 1 || len(res) != 1 || res[0].Out != out || errStr(res[0].Err) != errStr(err):
			t.Fatalf("RenderChain: step %d, %+v; Render: %q, %v", n, res, out, err)
		}
		if !errors.Is(err, ErrUnsupported) {
			if out2, err2 := raised.Render(tpl, vars, strict); out2 != out || errStr(err2) != errStr(err) {
				t.Fatalf("with raised limits and unknown filters passed: %q, %v; by default: %q, %v", out2, err2, out, err)
			}
		}
		if !reflect.DeepEqual(vars, newVars()) {
			t.Fatalf("vars mutated: %v", vars)
		}
	})
}

// runtimeBail reports data-dependent bails, which Check can't see.
func runtimeBail(err error) bool {
	var e *Error
	return errors.As(err, &e) && runtimeCodes[e.Code]
}

var runtimeCodes = map[string]bool{
	"array-filter-object": true, "array-length-property": true, "bad-json-number": true,
	"builtin-variable-name": true, "case-mapping": true, "contains-object": true, "date-input-type": true,
	"date-locale-format": true, "date-string-format": true, "date-timezone-argument": true,
	"date-timezone-name": true, "date-timezone-sub-minute": true, "date-timezone-unknown": true,
	"date-width": true, "date-year-range": true, "date-zone-name": true, "first-last-empty": true,
	"first-last-type": true, "for-over-non-array": true, "for-parameter-value": true, "group-by-object": true,
	"index-type": true, "invalid-utf8": true, "item-not-object": true, "iteration-limit": true,
	"join-nested-array": true, "json-type": true, "math-non-finite": true, "memory-limit": true,
	"nil-literal-argument": true, "number-from-string": true, "number-from-type": true,
	"numeric-argument": true, "object-equality": true, "ordering-types": true, "output-limit": true,
	"output-nil-literal-result": true, "output-type": true, "property-on-scalar": true, "property-path": true,
	"push-nil": true, "range-bound": true, "range-too-long": true, "round-digits": true,
	"same-value-object": true, "same-value-type": true, "sentence-type": true, "size-length-key": true,
	"slice-begin": true, "slugify-mode": true, "sort-astral": true, "sort-mixed": true, "sort-type": true,
	"split-surrogate-pair": true, "string-own-property": true, "string-type": true, "stringify-object": true,
	"strip-html-unclosed": true, "sum-non-finite": true, "sum-type": true,
	"to-integer-type": true, "unicode-version": true, "url-decode-malformed": true, "value-type": true,
	"where-computed-key": true, "where-input": true, "where-strict-missing": true,
}

// benchFixture loads testdata/bench/NAME: the template, its data, and liquidjs's output.
func benchFixture(tb testing.TB, name string) (tpl string, vars map[string]any, out string) {
	tb.Helper()
	read := func(ext string) []byte {
		b, err := os.ReadFile(filepath.Join("testdata", "bench", name+ext))
		if err != nil {
			tb.Fatal(err)
		}
		return b
	}
	if err := json.Unmarshal(read(".json"), &vars); err != nil {
		tb.Fatal(err)
	}
	return string(read(".liquid")), vars, string(read(".out"))
}

var benchFixtures = []string{"basic", "filters", "heavy", "modern"}

// benchEngine shares scripts/bench.mjs's clock, so fixtures using 'now' render the same.
var benchEngine = Engine{Now: func() time.Time { return time.UnixMilli(1700000000123) }}

// The fixtures render exactly as liquidjs does (scripts/bench.mjs -check), so timing them compares like with like.
func TestBenchFixturesMatchLiquidjs(t *testing.T) {
	for _, name := range benchFixtures {
		tpl, vars, want := benchFixture(t, name)
		if out, err := benchEngine.Render(tpl, vars, true); err != nil || out != want {
			t.Errorf("%s: got %q, %v; want %q", name, out, err, want)
		}
	}
}

// BenchmarkFixture is what scripts/bench.mjs compares with liquidjs.
func BenchmarkFixture(b *testing.B) {
	for _, name := range benchFixtures {
		tpl, vars, _ := benchFixture(b, name)
		b.Run(name, func(b *testing.B) {
			buf := make([]byte, 0, 4096)
			b.ReportAllocs()
			b.SetBytes(int64(len(tpl)))
			for b.Loop() {
				var err error
				if buf, err = benchEngine.Append(buf[:0], tpl, vars, true); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkRangeFor(b *testing.B) {
	for _, n := range []int{10, 1000, 10000} {
		tpl := fmt.Sprintf("{%% for i in (1..%d) %%}{{ i }}{%% endfor %%}", n)
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			buf := make([]byte, 0, 6*n)
			b.ReportAllocs()
			for b.Loop() {
				buf, _ = Append(buf[:0], tpl, nil, true)
			}
		})
	}
}

func BenchmarkRender(b *testing.B) {
	tpl, vars, _ := benchFixture(b, "basic")
	b.ReportAllocs()
	b.SetBytes(int64(len(tpl)))
	for b.Loop() {
		if _, err := Render(tpl, vars, true); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCheck(b *testing.B) {
	tpl, _, _ := benchFixture(b, "basic")
	b.ReportAllocs()
	for b.Loop() {
		if err := Check(tpl); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSmall(b *testing.B) {
	vars := map[string]any{"customer": map[string]any{"first_name": "Ada"}}
	buf := make([]byte, 0, 64)
	b.ReportAllocs()
	for b.Loop() {
		buf, _ = Append(buf[:0], "Hi {{ customer.first_name }}!", vars, true)
	}
}

func BenchmarkTagLookup(b *testing.B) {
	e := Engine{Tags: map[string]TagFunc{"t": func(dst []byte, t Tag) ([]byte, error) {
		v, _ := t.Lookup("customer.first_name")
		s, _ := v.(string)
		return append(dst, s...), nil
	}}}
	vars := map[string]any{"customer": map[string]any{"first_name": "Ada"}}
	buf := make([]byte, 0, 64)
	b.ReportAllocs()
	for b.Loop() {
		buf, _ = e.Append(buf[:0], "{% t %}", vars, true)
	}
}

// BenchmarkNesting renders ifs nested n deep; frames past the inline stack allocate.
func BenchmarkNesting(b *testing.B) {
	for _, n := range []int{4, 8, 9, 12, 16, 17} {
		tpl := strings.Repeat("{% if a %}", n) + "x" + strings.Repeat("{% endif %}", n)
		vars := map[string]any{"a": true}
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			buf := make([]byte, 0, 64)
			b.ReportAllocs()
			for b.Loop() {
				buf, _ = Append(buf[:0], tpl, vars, true)
			}
		})
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

func TestDeepNesting(t *testing.T) {
	// 20 nested loops, then ifs: loop variables, forloop and break past the inline frames.
	var b strings.Builder
	for i := range 20 {
		b.WriteString("{% for v" + string(rune('a'+i)) + " in (1..1) %}")
	}
	b.WriteString("{% for x in (1..3) %}{% if x == 2 %}{% break %}{% endif %}{{ va }}{{ vt }}{{ forloop.index }}{% endfor %}")
	for range 20 {
		b.WriteString("{% endfor %}")
	}
	b.WriteString(strings.Repeat("{% if true %}", 40) + "deep" + strings.Repeat("{% endif %}", 40))
	if out, err := Render(b.String(), nil, true); err != nil || out != "111deep" {
		t.Fatalf("got %q, %v", out, err)
	}
	if _, err := Render(strings.Repeat("{% if true %}", maxDepth+1), nil, false); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("got %v; want ErrUnsupported", err)
	}
	e := Engine{Tags: map[string]TagFunc{"show": func(dst []byte, t Tag) ([]byte, error) {
		v, _ := t.Lookup("x")
		return fmt.Appendf(dst, "%v", v), nil
	}}}
	tpl := strings.Repeat("{% if true %}", 20) + "{% for x in (7..7) %}{% show %}{% endfor %}" + strings.Repeat("{% endif %}", 20)
	if out, err := e.Render(tpl, nil, false); err != nil || out != "7" {
		t.Fatalf("got %q, %v", out, err)
	}
}

func TestPanicsBecomeErrInternal(t *testing.T) {
	boom := func() { panic("boom") }
	e := Engine{
		Filters: map[string]FilterFunc{"boom": func(Filter) (any, error) { boom(); return nil, nil }},
		Tags:    map[string]TagFunc{"boom": func([]byte, Tag) ([]byte, error) { boom(); return nil, nil }},
	}
	d := Engine{Dialect: &Dialect{Output: func([]byte, any) ([]byte, error) { boom(); return nil, nil }}}
	for _, c := range []struct {
		e   Engine
		tpl string
	}{{e, "ab{{ 1 | boom }}"}, {e, "ab{% boom %}"}, {d, "ab{{ 1 }}"}} {
		_, err := c.e.Render(c.tpl, nil, false)
		var me *Error
		if !errors.Is(err, ErrInternal) || !errors.Is(err, ErrUnsupported) || !errors.As(err, &me) || me.Pos < 2 || !strings.Contains(string(me.Stack), "TestPanicsBecomeErrInternal") {
			t.Fatalf("%s: got %v", c.tpl, err)
		}
	}
	// A chain falls back at the step that panicked.
	if res, n, _ := e.RenderChain([]Step{{Body: "ok"}, {Body: "{{ 1 | boom }}"}}, nil); n != 1 || len(res) != 1 {
		t.Fatalf("got %v, %d", res, n)
	}
}

// failInternal fails t on ErrInternal, which tests must never see: it's a recovered panic.
func failInternal(t *testing.T, err error) {
	t.Helper()
	if e := (*Error)(nil); errors.Is(err, ErrInternal) && errors.As(err, &e) {
		t.Fatalf("%v\n%s", err, e.Stack)
	}
}

func TestLimits(t *testing.T) {
	big := strings.Repeat("x", 10_000)
	xs := make([]any, 1000)
	for i := range xs {
		xs[i] = "a"
	}
	vars := map[string]any{"big": big, "xs": xs, "sep": strings.Repeat("-", 20_000), "f": strings.Repeat("%1000Y", 20_000), "n": 1e8}
	for _, c := range []struct {
		e   Engine
		tpl string
	}{
		{Engine{}, `{{ 'abc' | slice: 0, 100000000 }}`}, // liquidjs with render's memoryLimit throws
		{Engine{}, `{{ 'abc' | slice: 0, n }}`},
		{Engine{}, `{% assign s = big %}{% for i in (1..12) %}{% assign s = s | append: s %}{% endfor %}`},
		{Engine{}, `{{ xs | join: sep }}`},
		{Engine{}, `{{ big | replace: '', big }}`},
		{Engine{}, `{{ 0 | date: f }}`},
		{Engine{Limits: Limits{Output: 100}}, `{% for i in (1..50) %}xxx{% endfor %}`},
		{Engine{Limits: Limits{Output: 100}}, `{% capture c %}{% for i in (1..50) %}xxx{% endfor %}{% endcapture %}`},
		{Engine{Limits: Limits{Output: 100}}, `{% liquid for i in (1..50)
echo 'xxx'
endfor %}`},
		{Engine{Limits: Limits{Output: 100}}, big},
		{Engine{Limits: Limits{Output: 100}}, `{{ 1 }}` + big},
		{Engine{Limits: Limits{Iterations: 10}}, `{% for i in (1..12) %}{% endfor %}`},
	} {
		if _, err := c.e.Render(c.tpl, vars, false); !errors.Is(err, ErrUnsupported) || errors.Is(err, ErrInternal) {
			t.Errorf("%s: got %v; want ErrUnsupported", c.tpl, err)
		}
	}
	e := Engine{Limits: Limits{Memory: 200_000_000, Output: 150, Iterations: 59}}
	if out, err := e.Render(`{{ 'abc' | slice: 0, 100000000 }}{% for i in (1..12) %}{% endfor %}{% for i in (1..49) %}xxx{% endfor %}`, nil, false); err != nil || len(out) != 150 {
		t.Fatalf("raised limits: got %d bytes, %v", len(out), err)
	}
	// Output counts what this render writes, not what Append's dst already holds.
	if _, err := e.Append(make([]byte, 1000), `{{ 1 }}`+strings.Repeat("x", 149), nil, false); err != nil {
		t.Fatal(err)
	}
}

func TestErrorString(t *testing.T) {
	_, err := Render("ab{{ x | nope }}", nil, false)
	if want := `mist: unsupported at offset 9: unsupported filter "nope"`; err == nil || err.Error() != want {
		t.Fatalf("got %v; want %s", err, want)
	}
}

// Counters persist into the chain's vars even when the caller passed none.
func TestRenderChainCountersWithNilVars(t *testing.T) {
	res, n, vars := RenderChain([]Step{{Body: "{% increment c %}"}, {Body: "{{ c }}"}}, nil)
	if n != 2 || res[1].Out != "1" || vars["c"] != 1.0 {
		t.Fatalf("got %v, %d, %v", res, n, vars)
	}
}

// Pooled assigns maps come back empty, and big ones aren't pooled.
func TestAssignsDoNotOutliveRender(t *testing.T) {
	for _, n := range []int{1, maxPooledAssigns + 1} {
		var b strings.Builder
		for i := range n {
			fmt.Fprintf(&b, "{%% assign a%d = 1 %%}", i)
		}
		if _, err := Render(b.String(), nil, false); err != nil {
			t.Fatal(err)
		}
		if out, err := Render("{{ a0 }}", nil, false); err != nil || out != "" {
			t.Errorf("after %d assigns: got %q, %v; want empty", n, out, err)
		}
	}
}

// Invalid UTF-8 ends an identifier, so the parse fails where it stops.
func TestInvalidUTF8EndsIdentifier(t *testing.T) {
	if _, err := Render("{{ a.b\xff }}", map[string]any{"a": map[string]any{"b": 1}}, false); !errors.Is(err, ErrUnsupported) {
		t.Errorf("got %v; want ErrUnsupported", err)
	}
}
