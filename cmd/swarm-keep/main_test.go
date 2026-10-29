package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/petfold/swarm-keep/internal/beetest"
)

const (
	ref   = "2abb4979d03acd202fe547cdb1902b86b06bd9ce16c341cd9cfe4d9a2e6b5442"
	batch = "c931c8a5ee8def225abf86a934bbf38ab191f10456f1a684c9cb44f534834359"
)

func write(t *testing.T, dir, name, s string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func runIt(args ...string) (int, string, string) {
	var out, errs bytes.Buffer
	code := run(context.Background(), args, &out, &errs)
	return code, out.String(), errs.String()
}

func TestExitStatus(t *testing.T) {
	n := beetest.New(t)
	n.Tree(ref, true)
	dir := t.TempDir()
	p := write(t, dir, "k.json", `{"keep": 1, "name": "site", "batch": "`+batch+`", "items": [{"name": "page", "ref": "`+ref+`"}]}`)

	n.Batch(batch, 60*24*time.Hour)
	if code, out, errs := runIt("check", "-bee", n.URL, "-l", p); code != 0 || !strings.Contains(out, "1 items: 1 ok") {
		t.Errorf("fine: %d\n%s%s", code, out, errs)
	}
	n.Batch(batch, 3*24*time.Hour)
	if code, out, errs := runIt("check", "-bee", n.URL, "-l", dir); code != 1 || !strings.Contains(out, "WARN  page") {
		t.Errorf("running short: %d\n%s%s", code, out, errs)
	}

	code, out, _ := runIt("check", "-bee", n.URL, "-l", p, "-json")
	var rep struct {
		Status string
		Items  []struct {
			Status      string
			SecondsLeft int64 `json:"seconds_left"`
		}
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil || code != 1 || rep.Status != "warn" || rep.Items[0].SecondsLeft != 3*24*3600 {
		t.Errorf("json: %d %v %+v\n%s", code, err, rep, out)
	}

	if code, out, errs := runIt("where", "-bee", n.URL, "-l", p, ref); code != 1 || !strings.Contains(out, "kept by site: page") {
		t.Errorf("where, running short: %d\n%s%s", code, out, errs)
	}
	n.Batch(batch, 60*24*time.Hour)
	if code, out, errs := runIt("where", "-bee", n.URL, "-l", p, ref); code != 0 {
		t.Errorf("where, fine: %d\n%s%s", code, out, errs)
	}
}

func TestUsageErrors(t *testing.T) {
	dir := t.TempDir()
	bad := write(t, dir, "bad.json", `{"keep": 1, "name": "x", "items": [{"name": "a", "ref": "xyz"}]}`)
	good := write(t, dir, "good.json", `{"keep": 1, "name": "x", "items": [{"name": "a", "ref": "`+ref+`"}]}`)
	other := filepath.Join(t.TempDir(), "same-name.json")
	write(t, filepath.Dir(other), "same-name.json", `{"keep": 1, "name": "x", "items": [{"name": "a", "ref": "`+ref+`"}]}`)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, c := range []struct {
		args []string
		want string
	}{
		{nil, "Usage:"},
		{[]string{"turn"}, `unknown command "turn"`},
		{[]string{"rotate", "-l", good}, "-to wants a batch ID"},
		{[]string{"rotate", "-to", batch, "-verify", "some", "-l", good}, `-verify "some"`},
		{[]string{"check", "-l", bad}, "128 for encrypted"},
		{[]string{"check", "-l", good, "-l", other}, `both named "x"`},
		{[]string{"check"}, "no keep-lists in"},
		{[]string{"where", "-l", good}, "want one reference"},
		{[]string{"check", "-l", good, "extra"}, `unexpected "extra"`},
	} {
		code, _, errs := runIt(c.args...)
		if code != 2 || !strings.Contains(errs, c.want) {
			t.Errorf("%q: %d %q, want 2 and %q", c.args, code, errs, c.want)
		}
	}
	if code, out, _ := runIt("version"); code != 0 || out != "swarm-keep dev\n" {
		t.Errorf("version: %d %q", code, out)
	}
}

func TestRotateCommand(t *testing.T) {
	n := beetest.New(t)
	n.Tree(ref, true)
	n.Batch(batch, 5*24*time.Hour)
	fresh := strings.Repeat("0f", 32)
	n.OwnBatch(fresh, 60*24*time.Hour, beetest.Own{Usable: true, Immutable: true, Depth: 20, Room: -1})
	dir := t.TempDir()
	p := write(t, dir, "k.json", `{"keep": 1, "name": "site", "batch": "`+batch+`", "items": [{"name": "page", "ref": "`+ref+`"}]}`)
	log := filepath.Join(dir, "state", "rotations.jsonl")

	if code, out, errs := runIt("rotate", "-bee", n.URL, "-l", p, "-to", batch); code != 2 || !strings.Contains(errs, "is not this node's") || out != "" {
		t.Errorf("onto someone else's batch: %d\n%s%s", code, out, errs)
	}
	if code, out, errs := runIt("rotate", "-bee", n.URL, "-l", p, "-to", fresh, "-dry-run", "-log", log); code != 0 || !strings.Contains(out, "Would rotate") || !strings.Contains(out, "site (from c931c8a5…): 1 trees, 0 chunks\n") {
		t.Errorf("dry run: %d\n%s%s", code, out, errs)
	}
	code, out, errs := runIt("rotate", "-bee", n.URL, "-l", p, "-to", strings.ToUpper(fresh), "-verify", "all", "-log", log, "-json")
	var rot struct {
		To    string
		Lists []struct{ Moved bool }
	}
	if err := json.Unmarshal([]byte(out), &rot); err != nil || code != 0 || rot.To != fresh || !rot.Lists[0].Moved || !strings.Contains(errs, "re-stamped 1 of 1 (0 failed)") {
		t.Errorf("rotate: %d %v\n%s%s", code, err, out, errs)
	}
	if b, _ := os.ReadFile(log); strings.Count(string(b), "\n") != 1 { // a dry run is not logged
		t.Errorf("log: %s", b)
	}
	if code, out, _ := runIt("check", "-bee", n.URL, "-l", p); code != 0 || !strings.Contains(out, "about 60 days left") {
		t.Errorf("check after the rotation: %d\n%s", code, out)
	}
}
