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
		"{{ name | upcase | default: 'x' }}":                           {"filter:upcase", "filter:default"},
		"{{ a | upcase }} {{ b | upcase }}":                            {"filter:upcase"},
		"{% case x %}{% when 1 %}one{% else %}other{% endcase %}":      {"tag:case", "tag:when"},
		"{% if a contains 'b' %}y{% endif %}{{ c | escape }}":          {"contains", "filter:escape"},
		"{% for x in xs limit:2 %}{{ forloop.index }}{% endfor %}":     {"for:limit", "forloop"},
		"{% for i in (1..3) %}{{ i }}{% endfor %}":                     {"for:range"},
		"{% if a or b and c %}y{% endif %}":                            {"mixed and/or"},
		"{% if x == empty %}y{% endif %}":                              {"empty"},
		"{% unsubscribe_url %} {% capture x %}{{ y }}{% endcapture %}": {"tag:unsubscribe_url", "tag:capture"},
		"{{ 1.5 }}":   {"float literal"},
		"{% if x %}":  {"structure"},
		"{% endif %}": {"structure"},
		"{% if x %}{% for y in ys offset:1 %}{% endfor %}{% endif %}{{ z | a }}": {"for:offset", "filter:a"},
		`{{ x | default: "Don't miss | Wagering rules apply" }}`:                 {"filter:default"},
		`{{ "Offer ends | Valable jusqu'au lundi" | upcase }}`:                   {"filter:upcase"},
		`{{ 'He said "hi | there"' | escape }}`:                                  {"filter:escape"},
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
