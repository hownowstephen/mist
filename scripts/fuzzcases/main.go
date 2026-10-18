// Command fuzzcases turns Go fuzz corpus files (testdata/fuzz, or the fuzz cache under
// $(go env GOCACHE)/fuzz) into oracle.mjs input, so what fuzzing found gets a liquidjs
// differential. FuzzRender inputs get its fixed vars; FuzzInvariants inputs keep their
// data when it is a JSON object.
//
//	go run ./scripts/fuzzcases testdata/fuzz "$(go env GOCACHE)/fuzz" > /tmp/fuzz.json
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type fuzzCase struct {
	Src  string         `json:"src"`
	Name string         `json:"name"`
	Tpl  string         `json:"tpl"`
	Data map[string]any `json:"data"`
}

func main() {
	cases := []fuzzCase{}
	for _, root := range os.Args[1:] {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			lines := strings.Split(strings.TrimSpace(string(b)), "\n")
			if len(lines) < 2 || lines[0] != "go test fuzz v1" {
				return nil
			}
			tpl, ok := value(lines[1], "string(")
			if !ok {
				return nil
			}
			c := fuzzCase{Src: "fuzz/" + filepath.Base(filepath.Dir(path)), Name: d.Name(), Tpl: tpl}
			if len(lines) > 2 {
				if data, ok := value(lines[2], "[]byte("); ok {
					d := json.NewDecoder(bytes.NewReader([]byte(data)))
					if d.Decode(&c.Data) != nil || d.More() {
						c.Data = nil
					}
				}
			}
			if c.Data == nil {
				// FuzzRender's vars; FuzzInvariants falls back to them too.
				c.Data = map[string]any{"a": map[string]any{"b": "x", "n": 2.0}, "xs": []any{1.0, "s", nil}, "t": true, "s": "str"}
			}
			cases = append(cases, c)
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	if err := json.NewEncoder(os.Stdout).Encode(cases); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "%d fuzz inputs\n", len(cases))
}

// value decodes a corpus line such as string("...").
func value(line, prefix string) (string, bool) {
	q, ok := strings.CutPrefix(line, prefix)
	if !ok || !strings.HasSuffix(q, ")") {
		return "", false
	}
	s, err := strconv.Unquote(q[:len(q)-1])
	return s, err == nil
}
