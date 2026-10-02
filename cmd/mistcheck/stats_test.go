package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/hownowstephen/mist"
)

func TestBlockers(t *testing.T) {
	for tpl, want := range map[string][]string{
		"Hi {{ name }}{% if x %}y{% endif %}":                      nil,
		"{{ name | md5 | default: 'x' }}":                          {"filter:md5"},
		"{{ a | md5 }} {{ b | md5 }}":                              {"filter:md5"},
		"{% tablerow i in xs %}{% cycle 1, 2 %}{% endtablerow %}":  {"tag:tablerow", "tag:cycle"},
		"{% if a %}{% cycle 'b' %}{% endif %}{{ c | xml_escape }}": {"tag:cycle", "filter:xml_escape"},
		"{% for x in xs %}{% cycle 1, 2 %}{% endfor %}":            {"tag:cycle"},
		"{{ 1.2.3 }}": {"malformed number"},
		"{{ empty }}": {"empty outside ==/!="},
		"{% unsubscribe_url %} {% increment y %}": {"tag:unsubscribe_url", "tag:increment"},
		`{{ "\uD83D" }}`: {"string escapes"},
		"{% if x %}":     {"structure"},
		"{% endif %}":    {"structure"},
		"{% if x %}{% for y in ys %}{% cycle 1 %}{% endfor %}{% endif %}{{ z | a }}": {"tag:cycle", "filter:a"},
		`{{ x | default: "Don't stop | keep going" | sha1: 5 }}`:                     {"filter:sha1"},
		`{{ "Ends soon | C'est la fin" | md5 }}`:                                     {"filter:md5"},
		`{{ 'He said "hi | there"' | xml_escape }}`:                                  {"filter:xml_escape"},
		`{% if a == "x contains y" %}{{ b | c }}{% endif %}`:                         {"filter:c"},
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
	if got := blockers(e, "{{ a | titlecase | capitalize | sha1: 3 }}"); !slices.Equal(got, []string{"filter:sha1"}) {
		t.Errorf("got %q; want only filter:sha1", got)
	}
}

func TestReport(t *testing.T) {
	out := report([][]string{nil, {"filter:default"}, {"filter:default", "tag:case"}, {"tag:case"}, {"tag:case"}})
	for _, want := range []string{
		"5 templates, 1 in spec (20.0%)",
		"tag:case                                  3            2",
		"filter:default                            2            1",
		"filter:default + tag:case",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q:\n%s", want, out)
		}
	}
	if strings.Index(out, "tag:case") > strings.Index(out, "filter:default") {
		t.Errorf("want rows sorted by templates it alone blocks:\n%s", out)
	}
}

func TestReportCountsBlockersThatOnlyAppearTogether(t *testing.T) {
	out := report([][]string{{"tag:x", "tag:y"}, {"tag:y", "tag:x"}, {"tag:x", "tag:y", "filter:z"}})
	want := "tag:x + tag:y                                                       2"
	if !strings.Contains(out, want) {
		t.Errorf("report missing %q:\n%s", want, out)
	}
}
