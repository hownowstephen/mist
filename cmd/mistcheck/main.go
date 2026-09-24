// Command mistcheck reports whether Liquid templates are inside the mist subset.
//
//	mistcheck FILE...   (reads stdin when no files are given)
//
// Exits 1 if any template is out of spec.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/hownowstephen/mist"
)

func main() {
	files := os.Args[1:]
	if len(files) == 0 {
		files = []string{"-"}
	}
	status := 0
	for _, f := range files {
		var b []byte
		var err error
		if f == "-" {
			b, err = io.ReadAll(os.Stdin)
		} else {
			b, err = os.ReadFile(f)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		tpl := string(b)
		var e *mist.Error
		if err := mist.Check(tpl); errors.As(err, &e) {
			line := strings.Count(tpl[:e.Pos], "\n") + 1
			col := e.Pos - strings.LastIndexByte(tpl[:e.Pos], '\n')
			fmt.Printf("%s:%d:%d: %s\n", f, line, col, e.Msg)
			status = 1
		}
	}
	os.Exit(status)
}
