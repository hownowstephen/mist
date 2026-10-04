package mist_test

import (
	"errors"
	"fmt"
	"strings"

	"github.com/hownowstephen/mist"
)

func ExampleRender() {
	out, err := mist.Render("Hi {{ name | capitalize }}!", map[string]any{"name": "ada"}, true)
	fmt.Println(out, err)
	// Output: Hi Ada! <nil>
}

// Templates outside the subset return ErrUnsupported; render those with the full engine.
func ExampleRender_fallback() {
	_, err := mist.Render("{% increment n %}", nil, false)
	if errors.Is(err, mist.ErrUnsupported) {
		fmt.Println("render with the full engine")
	}
	// Output: render with the full engine
}

func ExampleEngine_customFilter() {
	e := mist.Engine{Filters: map[string]mist.FilterFunc{
		"shout": func(f mist.Filter) (any, error) {
			s, ok := f.Input.(string)
			if !ok {
				return nil, mist.ErrUnsupported
			}
			return strings.ToUpper(s) + "!", nil
		},
	}}
	out, _ := e.Render("{{ greeting | shout }}", map[string]any{"greeting": "hello"}, true)
	fmt.Println(out)
	// Output: HELLO!
}

func ExampleEngine_customTag() {
	e := mist.Engine{Tags: map[string]mist.TagFunc{
		"link": func(dst []byte, t mist.Tag) ([]byte, error) {
			url, ok := t.Lookup(t.Args)
			if !ok {
				return nil, mist.ErrUnsupported
			}
			return fmt.Appendf(dst, `<a href="%s">`, url), nil
		},
	}}
	out, _ := e.Render("{% for p in products %}{% link p.url %}{% endfor %}",
		map[string]any{"products": []any{map[string]any{"url": "/a"}, map[string]any{"url": "/b"}}}, true)
	fmt.Println(out)
	// Output: <a href="/a"><a href="/b">
}

func ExampleCheck() {
	fmt.Println(mist.Check("{% if a %}{{ a | upcase }}{% endif %}"))
	fmt.Println(errors.Is(mist.Check("{% tablerow x in xs %}{% endtablerow %}"), mist.ErrUnsupported))
	// Output:
	// <nil>
	// true
}
