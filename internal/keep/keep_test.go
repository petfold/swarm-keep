package keep_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/petfold/swarm-keep/internal/bee"
	"github.com/petfold/swarm-keep/internal/beetest"
	"github.com/petfold/swarm-keep/internal/feed"
	"github.com/petfold/swarm-keep/internal/keep"
	"github.com/petfold/swarm-keep/internal/keeplist"
)

const day = 24 * time.Hour

func hexOf(b byte, n int) string { return hex.EncodeToString(bytes.Repeat([]byte{b}, n)) }

var (
	refA   = hexOf(0xa1, 32)
	refB   = hexOf(0xb2, 32)
	refC   = hexOf(0xc3, 32)
	long   = hexOf(0x01, 32) // a batch with 30 days left
	short  = hexOf(0x02, 32) // 5 days
	gone   = hexOf(0x03, 32) // not on chain
	topic  = bytes.Repeat([]byte{9}, 32)
	sample = keep.Options{Sample: 100, Workers: 4}
)

func list(t *testing.T, json string) *keeplist.List {
	t.Helper()
	l, err := keeplist.Parse([]byte(json))
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func setup(t *testing.T) (*beetest.Node, *keep.Checker) {
	n := beetest.New(t)
	n.Batch(long, 30*day)
	n.Batch(short, 5*day)
	b := bee.New(n.URL, 4)
	return n, &keep.Checker{Bee: b, Expand: &keep.Expander{Bee: b}}
}

func TestCheckStatuses(t *testing.T) {
	n, c := setup(t)
	n.Tree(refA, true)
	n.Tree(refB, false)
	l := list(t, `{"keep": 1, "name": "data", "batch": "`+long+`", "items": [
	  {"name": "fine", "ref": "`+refA+`"},
	  {"name": "running short", "ref": "`+refA+`", "batch": "`+short+`"},
	  {"name": "expired", "ref": "`+refA+`", "batch": "`+gone+`"},
	  {"name": "lost", "ref": "`+refB+`"},
	  {"name": "never uploaded", "ref": "`+refC+`"}]}`)
	l2 := list(t, `{"keep": 1, "name": "unrecorded", "items": [{"name": "no batch", "ref": "`+refA+`"}]}`)
	rep, err := c.Check(context.Background(), []*keeplist.List{l, l2}, sample)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]keep.Status{"fine": keep.OK, "running short": keep.Warn, "expired": keep.Fail, "lost": keep.Fail, "never uploaded": keep.Fail, "no batch": keep.Warn}
	for _, r := range rep.Items {
		if r.Status != want[r.Item] {
			t.Errorf("%s: %s %q, want %s", r.Item, r.Status, r.Problems, want[r.Item])
		}
	}
	if rep.Status != keep.Fail {
		t.Errorf("report status %s", rep.Status)
	}
	fine := rep.Items[0]
	if d, ok := fine.TimeLeft(); !ok || d != 30*day {
		t.Errorf("fine: %v left, want 30 days", d)
	}
	if fine.BalanceLeft != "51840000" { // 30 days of 5 s blocks at 100 PLUR
		t.Errorf("fine: balance %s", fine.BalanceLeft)
	}
	if p := rep.Items[1].Problems; len(p) != 1 || !strings.Contains(p[0], "about 5 days left on batch 02020202…, under the list's warning time of 14 days") {
		t.Errorf("running short: %q", p)
	}
	if m := rep.Items[3].Missing; len(m) != 1 || m[0] != refB {
		t.Errorf("lost: missing %v", m)
	}

	var text bytes.Buffer
	rep.Text(&text)
	for _, s := range []string{"6 items: 1 ok, 2 warn, 3 fail", "\ndata\n", "  OK    fine", "about 30 days left", "missing: " + refB, "\nunrecorded\n", "time unknown"} {
		if !strings.Contains(text.String(), s) {
			t.Errorf("the text lacks %q:\n%s", s, text.String())
		}
	}
	sum := rep.Summary()
	if !strings.HasPrefix(sum, "5 items need attention: expired (") || !strings.Contains(sum, "lost (not retrievable)") || !strings.Contains(sum, "running short (about 5 days left)") {
		t.Errorf("summary %q", sum)
	}
}

func TestFeedKeepsEveryUpdateAndTheLatestVersions(t *testing.T) {
	n, c := setup(t)
	refs := []string{hexOf(0x10, 32), hexOf(0x11, 32), hexOf(0x12, 32), hexOf(0x13, 32)}
	var payloads [][]byte
	for _, r := range refs {
		b, _ := hex.DecodeString(r)
		payloads = append(payloads, beetest.LegacyPayload(b))
	}
	owner := n.Feed(t, topic, 2, payloads...)
	f := feed.Feed{Owner: owner, Topic: topic}
	l := list(t, `{"keep": 1, "name": "site", "batch": "`+long+`", "items": [
	  {"name": "feed", "feed": {"owner": "`+hex.EncodeToString(owner[:])+`", "topic": "`+hex.EncodeToString(topic)+`", "manifest": "`+refA+`"}},
	  {"name": "all", "versions": "all", "feed": {"owner": "`+hex.EncodeToString(owner[:])+`", "topic": "`+hex.EncodeToString(topic)+`"}}]}`)
	ts, err := c.Expand.Expand(context.Background(), l, &l.Items[0])
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, x := range ts {
		got = append(got, x.Kind.String()+" "+x.Addr[:4]+" "+x.Why)
	}
	want := []string{"ref " + refA[:4] + " feed manifest"}
	for i := range 4 {
		a, _ := f.Address(uint64(i))
		want = append(want, "chunk "+a.String()[:4]+" update "+string(rune('0'+i)))
		if i >= 2 { // the latest and one before it
			want = append(want, "ref "+refs[i][:4]+" version "+string(rune('1'+i))+" of 4")
		}
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("targets:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	ts, err = c.Expand.Expand(context.Background(), l, &l.Items[1])
	if err != nil {
		t.Fatal(err)
	}
	if len(ts) != 8 {
		t.Errorf(`versions "all": %d targets, want 4 updates and 4 versions`, len(ts))
	}
}

func TestListCommandAndWalk(t *testing.T) {
	_, c := setup(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "refs.txt"), []byte("# kept\n"+refA+"\nchunk "+refB+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "k.json"), []byte(`{"keep": 1, "name": "l", "items": [
	  {"name": "list", "list": "refs.txt"},
	  {"name": "cmd", "cmd": ["export"]},
	  {"name": "walked", "ref": "`+refA+`", "walk": ["walker", "--root={ref}"]},
	  {"name": "failing", "cmd": ["fail"]}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	l, err := keeplist.Load(filepath.Join(dir, "k.json"))
	if err != nil {
		t.Fatal(err)
	}
	var ran []string
	c.Expand.Run = func(_ context.Context, argv []string, d string) ([]byte, error) {
		ran = append(ran, strings.Join(argv, " ")+" in "+filepath.Base(d))
		switch argv[0] {
		case "export":
			return []byte(refC + "\n"), nil
		case "walker":
			return []byte(refA + "\n" + refB + "\nchunk " + refC + "\n"), nil
		}
		return nil, os.ErrPermission
	}
	expand := func(i int) string {
		ts, err := c.Expand.Expand(context.Background(), l, &l.Items[i])
		if err != nil {
			return "error: " + err.Error()
		}
		var s []string
		for _, x := range ts {
			s = append(s, x.Kind.String()+" "+x.Addr[:2]+" "+x.Why)
		}
		return strings.Join(s, ", ")
	}
	for i, want := range []string{
		"ref a1 , chunk b2 ",
		"ref c3 ",
		"ref a1 , ref b2 walk, chunk c3 walk",
		"error: fail: permission denied",
	} {
		if got := expand(i); got != want {
			t.Errorf("%s: %q, want %q", l.Items[i].Name, got, want)
		}
	}
	if strings.Join(ran, "; ") != "export in "+filepath.Base(dir)+"; walker --root="+refA+" in "+filepath.Base(dir)+"; fail in "+filepath.Base(dir) {
		t.Errorf("ran %q", ran)
	}
}

func TestSample(t *testing.T) {
	n, c := setup(t)
	var lines []string
	for i := range 20 {
		r := hexOf(byte(0x40+i), 32)
		n.Tree(r, true)
		lines = append(lines, r)
	}
	c.Expand.Run = func(context.Context, []string, string) ([]byte, error) {
		return []byte(strings.Join(lines, "\n")), nil
	}
	l := list(t, `{"keep": 1, "name": "l", "batch": "`+long+`", "items": [{"name": "many", "cmd": ["x"]}]}`)
	rep, err := c.Check(context.Background(), []*keeplist.List{l}, keep.Options{Sample: 5, Workers: 3, Rand: rand.New(rand.NewPCG(1, 2))})
	if err != nil {
		t.Fatal(err)
	}
	if r := rep.Items[0]; r.Targets != 20 || r.Checked != 5 || r.Status != keep.OK {
		t.Errorf("%d kept, %d checked, %s", r.Targets, r.Checked, r.Status)
	}
	asked := 0
	first := false
	for _, q := range n.Requests {
		if strings.HasPrefix(q, "GET /stewardship/") {
			asked++
			first = first || q == "GET /stewardship/"+lines[0]
		}
	}
	if asked != 5 || !first {
		t.Errorf("asked %d, the first among them %v", asked, first)
	}
	rep, err = c.Check(context.Background(), []*keeplist.List{l}, keep.Options{Sample: 5, Deep: true, Workers: 3})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Items[0].Checked != 20 {
		t.Errorf("deep: %d checked", rep.Items[0].Checked)
	}
}

func TestWhere(t *testing.T) {
	n, c := setup(t)
	n.Tree(refB, true)
	b, _ := hex.DecodeString(refB)
	owner := n.Feed(t, topic, 0, beetest.LegacyPayload(b))
	l := list(t, `{"keep": 1, "name": "site", "batch": "`+short+`", "items": [
	  {"name": "page", "feed": {"owner": "`+hex.EncodeToString(owner[:])+`", "topic": "`+hex.EncodeToString(topic)+`"}}]}`)
	w, err := c.Where(context.Background(), []*keeplist.List{l}, strings.ToUpper(refB))
	if err != nil {
		t.Fatal(err)
	}
	if !w.Found || len(w.Kept) != 1 || w.Kept[0].Why != "version 1 of 1" || w.Kept[0].Batch != short {
		t.Fatalf("%+v", w)
	}
	var text bytes.Buffer
	w.Text(&text)
	for _, s := range []string{"retrievable now", "kept by site: page, version 1 of 1", "batch " + short + ", about 5 days left", "8640000 PLUR per chunk left"} {
		if !strings.Contains(text.String(), s) {
			t.Errorf("the text lacks %q:\n%s", s, text.String())
		}
	}

	w, err = c.Where(context.Background(), []*keeplist.List{l}, refC)
	if err != nil {
		t.Fatal(err)
	}
	text.Reset()
	w.Text(&text)
	if w.Found || len(w.Kept) != 0 || !strings.Contains(text.String(), "NOT retrievable") || !strings.Contains(text.String(), "No keep-list keeps it") {
		t.Errorf("%+v\n%s", w, text.String())
	}
}

func TestAbout(t *testing.T) {
	for d, want := range map[time.Duration]string{
		0: "nothing", 20 * time.Hour: "about 20 hours", 47 * time.Hour: "about 47 hours",
		252 * time.Hour: "about 10.5 days", 5 * day: "about 5 days", 30*day + 3*time.Hour: "about 30 days",
	} {
		if got := keep.About(d); got != want {
			t.Errorf("%v: %q, want %q", d, got, want)
		}
	}
}
