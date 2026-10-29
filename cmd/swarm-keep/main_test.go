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
		{[]string{"rotate"}, `unknown command "rotate"`},
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
