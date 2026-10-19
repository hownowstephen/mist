package mist

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// FuzzParity is TestCorpus's differential on fuzzed templates and data, with liquidjs
// running in scripts/parity-worker.mjs, so the fuzzer can minimise a mismatch itself.
// It needs Node and npm ci in scripts, so runs only with MIST_FUZZ_PARITY=1:
//
//	MIST_FUZZ_PARITY=1 go test -run '^$' -fuzz FuzzParity -fuzztime 10m .
func FuzzParity(f *testing.F) {
	if os.Getenv("MIST_FUZZ_PARITY") == "" {
		f.Skip("set MIST_FUZZ_PARITY=1 to fuzz against liquidjs")
	}
	for _, c := range loadCases(f) {
		b := []byte("{}")
		if c.Data != nil {
			b, _ = json.Marshal(c.Data)
		}
		f.Add(c.Tpl, b, c.Strict)
	}
	b, err := os.ReadFile("testdata/corpus.json")
	if err != nil {
		f.Fatal(err)
	}
	var cs []corpusCase
	if err := json.Unmarshal(b, &cs); err != nil {
		f.Fatal(err)
	}
	for _, c := range cs {
		b, _ := json.Marshal(c.Data)
		f.Add(c.Tpl, b, false)
		f.Add(c.Tpl, b, true)
	}
	var js jsWorker
	f.Cleanup(js.close)
	f.Fuzz(func(t *testing.T, tpl string, data []byte, strict bool) {
		var vars map[string]any
		if json.Unmarshal(data, &vars) != nil || vars == nil {
			vars = map[string]any{"a": map[string]any{"b": "x", "n": 2.0}, "xs": []any{1.0, "s", nil}, "t": true, "s": "str"}
		}
		if !utf8.ValidString(tpl) {
			return // JSON can't carry it to liquidjs intact
		}
		out, err := corpusEngine.Render(tpl, vars, strict)
		failInternal(t, err)
		if errors.Is(err, ErrUnsupported) {
			return
		}
		want, ok := js.render(t, tpl, vars, strict)
		switch {
		case !ok, strings.Contains(want.Err, "limit"): // liquidjs timed out or hit a resource limit
		case err == nil && want.Out != nil && *want.Out == out:
		case errors.Is(err, ErrUndefined) && want.Err != "":
		default:
			wantS := want.Err
			if want.Out != nil {
				wantS = *want.Out
			}
			t.Fatalf("strict=%v\n  tpl:  %q\n  data: %s\n  mist: %q, %v\n  liquidjs: %q", strict, tpl, data, out, err, wantS)
		}
	})
}

// jsWorker is a running scripts/parity-worker.mjs, started on first use.
type jsWorker struct {
	cmd   *exec.Cmd
	in    io.WriteCloser
	lines chan []byte
}

// render reports false if liquidjs took too long, and restarts the worker.
func (w *jsWorker) render(t *testing.T, tpl string, vars map[string]any, strict bool) (oracle, bool) {
	if w.cmd == nil {
		w.start(t)
	}
	req, err := json.Marshal(map[string]any{"tpl": tpl, "data": vars, "strict": strict})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.in.Write(append(req, '\n')); err != nil {
		t.Fatal(err)
	}
	select {
	case line, ok := <-w.lines:
		if !ok {
			t.Fatal("parity-worker.mjs exited")
		}
		var o oracle
		if err := json.Unmarshal(line, &o); err != nil {
			t.Fatal(err)
		}
		return o, true
	case <-time.After(10 * time.Second):
		w.close()
		return oracle{}, false
	}
}

func (w *jsWorker) start(t *testing.T) {
	cmd := exec.Command("node", "scripts/parity-worker.mjs")
	cmd.Stderr = os.Stderr
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	lines := make(chan []byte)
	go func() {
		s := bufio.NewScanner(stdout)
		s.Buffer(nil, 1<<30)
		for s.Scan() {
			lines <- bytes.Clone(s.Bytes())
		}
		close(lines)
	}()
	w.cmd, w.in, w.lines = cmd, in, lines
}

func (w *jsWorker) close() {
	if w.cmd != nil {
		_ = w.in.Close()
		_ = w.cmd.Process.Kill()
		_ = w.cmd.Wait()
		w.cmd = nil
	}
}
