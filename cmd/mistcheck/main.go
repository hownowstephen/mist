// Command mistcheck reports whether Liquid templates are inside the mist subset.
//
//	mistcheck [-stats] FILE...   (reads stdin when no files are given)
//
// A .jsonl file holds one template per line as a JSON string. By default each
// out-of-spec template's first unsupported construct is printed and the exit status
// is 1. With -stats, every unsupported construct is found and a summary of how many
// templates each one blocks is printed instead.
package main

import (
	"bufio"
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
	flag.Parse()
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
				all = append(all, blockers(t.body))
				continue
			}
			if e, ok := errors.AsType[*mist.Error](mist.Check(t.body)); ok {
				line := strings.Count(t.body[:e.Pos], "\n") + 1
				col := e.Pos - strings.LastIndexByte(t.body[:e.Pos], '\n')
				fmt.Printf("%s:%d:%d: %s\n", t.name, line, col, e.Msg)
				status = 1
			}
		}
	}
	if *stats {
		report(os.Stdout, all)
	}
	os.Exit(status)
}

func read(f string) ([]template, error) {
	var r io.Reader = os.Stdin
	if f != "-" {
		file, err := os.Open(f)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		r = file
	}
	if !strings.HasSuffix(f, ".jsonl") {
		b, err := io.ReadAll(r)
		return []template{{f, string(b)}}, err
	}
	var tpls []template
	sc := bufio.NewScanner(r)
	sc.Buffer(nil, 64<<20)
	for n := 1; sc.Scan(); n++ {
		if strings.TrimSpace(sc.Text()) == "" {
			continue
		}
		var body string
		if err := json.Unmarshal(sc.Bytes(), &body); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", f, n, err)
		}
		tpls = append(tpls, template{fmt.Sprintf("%s:%d", f, n), body})
	}
	return tpls, sc.Err()
}
