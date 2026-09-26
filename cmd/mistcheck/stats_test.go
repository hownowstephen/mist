package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/hownowstephen/mist"
)

func TestBlockers(t *testing.T) {
	for tpl, want := range map[string][]string{
		"Hi {{ name }}{% if x %}y{% endif %}":                          nil,
		"{{ name | strip_html | default: 'x' }}":                       {"filter:strip_html"},
		"{{ a | strip_html }} {{ b | strip_html }}":                    {"filter:strip_html"},
		"{% case x %}{% when 1 %}one{% else %}other{% endcase %}":      {"tag:case", "tag:when"},
		"{% if a contains 'b' %}y{% endif %}{{ c | xml_escape }}":      {"contains", "filter:xml_escape"},
		"{% for x in xs limit:2 %}{{ forloop.index }}{% endfor %}":     {"for:limit", "forloop"},
		"{% for i in (1..3) %}{{ i }}{% endfor %}":                     {"for:range"},
		"{% if a or b and c %}y{% endif %}":                            {"mixed and/or"},
		"{% if x == empty %}y{% endif %}":                              {"empty"},
		"{% unsubscribe_url %} {% capture x %}{{ y }}{% endcapture %}": {"tag:unsubscribe_url", "tag:capture"},
		"{{ 1.5 }}":   {"float literal"},
		"{% if x %}":  {"structure"},
		"{% endif %}": {"structure"},
		"{% if x %}{% for y in ys offset:1 %}{% endfor %}{% endif %}{{ z | a }}": {"for:offset", "filter:a"},
		`{{ x | default: "Don't stop | keep going" | join: 5 }}`:                 {"filter:join"},
		`{{ "Ends soon | C'est la fin" | strip_html }}`:                          {"filter:strip_html"},
		`{{ 'He said "hi | there"' | xml_escape }}`:                              {"filter:xml_escape"},
		`{% if a == "x contains y" %}{{ b | c }}{% endif %}`:                     {"filter:c"},
	} {
		if got := blockers(mist.Engine{}, tpl); !slices.Equal(got, want) {
			t.Errorf("%s:\n got  %q\n want %q", tpl, got, want)
		}
	}
}

func TestBlockersWithTags(t *testing.T) {
	e := mist.Engine{Tags: map[string]mist.TagFunc{"unsubscribe_url": nil}}
	if got := blockers(e, "{% unsubscribe_url %}{{ a | b }}"); !slices.Equal(got, []string{"filter:b"}) {
		t.Errorf("got %q; want only filter:b", got)
	}
}

func TestBlockersWithFilters(t *testing.T) {
	e := mist.Engine{Filters: map[string]mist.FilterFunc{"titlecase": nil}}
	if got := blockers(e, "{{ a | titlecase | capitalize | join: 3 }}"); !slices.Equal(got, []string{"filter:join"}) {
		t.Errorf("got %q; want only filter:join", got)
	}
}

func TestReport(t *testing.T) {
	out := report([][]string{nil, {"filter:default"}, {"filter:default", "tag:case"}, {"tag:case"}, {"tag:case"}})
	for _, want := range []string{
		"5 templates, 1 in spec (20.0%)",
		"tag:case                                  3            2",
		"filter:default                            2            1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q:\n%s", want, out)
		}
	}
	if strings.Index(out, "tag:case") > strings.Index(out, "filter:default") {
		t.Errorf("want rows sorted by templates it alone blocks:\n%s", out)
	}
}
