// Command mistcheck reports whether Liquid templates are inside the mist subset.
//
//	mistcheck [-stats] [-tags a,b] [-filters a,b] FILE...   (reads stdin when no files are given)
//
// A .jsonl file holds one template per line as a JSON string. By default each
// out-of-spec template's first unsupported construct is printed and the exit status
// is 1. With -stats, every unsupported construct is found and a summary of how many
// templates each one blocks is printed instead.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/hownowstephen/mist"
)

type template struct {
	name, body string
}

func main() {
	stats := flag.Bool("stats", false, "summarize every unsupported construct across templates")
	tags := flag.String("tags", "", "comma-separated custom tag names to treat as registered")
	filters := flag.String("filters", "", "comma-separated custom filter names to treat as registered")
	flag.Parse()
	var e mist.Engine
	if *tags != "" {
		e.Tags = map[string]mist.TagFunc{}
		for name := range strings.SplitSeq(*tags, ",") {
			e.Tags[strings.TrimSpace(name)] = nil
		}
	}
	if *filters != "" {
		e.Filters = map[string]mist.FilterFunc{}
		for name := range strings.SplitSeq(*filters, ",") {
			e.Filters[strings.TrimSpace(name)] = nil
		}
	}
	files := flag.Args()
	if len(files) == 0 {
		files = []string{"-"}
	}

	var all [][]string
	status := 0
	for _, f := range files {
		tpls, err := read(f)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		for _, t := range tpls {
			if *stats {
				all = append(all, blockers(e, t.body))
				continue
			}
			if err, ok := errors.AsType[*mist.Error](e.Check(t.body)); ok {
				line := strings.Count(t.body[:err.Pos], "\n") + 1
				col := err.Pos - strings.LastIndexByte(t.body[:err.Pos], '\n')
				fmt.Printf("%s:%d:%d: %s\n", t.name, line, col, err.Msg)
				status = 1
			}
		}
	}
	if *stats {
		fmt.Print(report(all))
	}
	os.Exit(status)
}

func read(f string) ([]template, error) {
	var b []byte
	var err error
	if f == "-" {
		b, err = io.ReadAll(os.Stdin)
	} else {
		b, err = os.ReadFile(f)
	}
	if err != nil || !strings.HasSuffix(f, ".jsonl") {
		return []template{{f, string(b)}}, err
	}
	var tpls []template
	for i, line := range bytes.Split(b, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var body string
		if err := json.Unmarshal(line, &body); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", f, i+1, err)
		}
		tpls = append(tpls, template{fmt.Sprintf("%s:%d", f, i+1), body})
	}
	return tpls, nil
}
