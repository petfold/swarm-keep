package keep_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ethersphere/bee/v2/pkg/cac"

	"github.com/petfold/swarm-keep/internal/beetest"
	"github.com/petfold/swarm-keep/internal/feed"
	"github.com/petfold/swarm-keep/internal/keep"
	"github.com/petfold/swarm-keep/internal/keeplist"
)

var fresh = hexOf(0x0f, 32) // the batch rotations move onto

// rotation sets up a node with two lists on the batch "short": a site (a
// feed of three versions with its manifest, and a page uploaded with
// erasure coding) and a store (a tree and one content-addressed chunk).
func rotation(t *testing.T) (*beetest.Node, *keep.Checker, []*keeplist.List, feed.Feed) {
	n, c := setup(t)
	n.OwnBatch(fresh, 60*day, beetest.Own{Usable: true, Immutable: true, Depth: 20, Room: -1})
	var payloads [][]byte
	for _, r := range []string{refA, refB, refC} {
		n.Tree(r, true)
		b, _ := hex.DecodeString(r)
		payloads = append(payloads, beetest.LegacyPayload(b))
	}
	owner := n.Feed(t, topic, 0, payloads...)
	manifest, page, tree := hexOf(0xd4, 32), hexOf(0xe5, 32), hexOf(0xf6, 32)
	for _, r := range []string{manifest, page, tree} {
		n.Tree(r, true)
	}
	ch, err := cac.New([]byte("a chunk on its own"))
	if err != nil {
		t.Fatal(err)
	}
	n.Chunk(ch)
	dir := t.TempDir()
	site := filepath.Join(dir, "site.json")
	store := filepath.Join(dir, "store.json")
	os.WriteFile(site, []byte(`{"keep": 1, "name": "site", "note": "why we keep it", "batch": "`+short+`", "items": [
	  {"name": "feed", "feed": {"owner": "`+hex.EncodeToString(owner[:])+`", "topic": "`+hex.EncodeToString(topic)+`", "manifest": "`+manifest+`"}},
	  {"name": "page", "ref": "`+page+`", "redundancy": 2, "batch": "`+long+`", "note": "the page"}]}`), 0o600)
	os.WriteFile(store, []byte(`{"keep": 1, "name": "store", "batch": "`+short+`", "items": [
	  {"name": "tree", "ref": "`+tree+`"},
	  {"name": "chunk", "chunk": "`+ch.Address().String()+`"}]}`), 0o644)
	var ls []*keeplist.List
	for _, p := range []string{site, store} {
		l, err := keeplist.Load(p)
		if err != nil {
			t.Fatal(err)
		}
		ls = append(ls, l)
	}
	return n, c, ls, feed.Feed{Owner: owner, Topic: topic}
}

func stampingRequests(n *beetest.Node) []string {
	var out []string
	for _, q := range n.Requests {
		if strings.HasPrefix(q, "PUT ") || strings.HasPrefix(q, "POST ") {
			out = append(out, q)
		}
	}
	return out
}

func TestRotate(t *testing.T) {
	n, c, ls, f := rotation(t)
	log := filepath.Join(t.TempDir(), "state", "rotations.jsonl")
	var progress []int
	rot, err := c.Rotate(context.Background(), ls, keep.RotateOptions{
		To: fresh, Verify: keep.VerifyAll, Workers: 3, Log: log,
		Progress: func(done, total, failed int) {
			if total != 9 || failed != 0 {
				t.Errorf("progress %d of %d, %d failed", done, total, failed)
			}
			progress = append(progress, done)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !rot.OK() {
		t.Fatalf("%+v", rot)
	}
	if len(progress) != 9 {
		t.Errorf("progress called %d times", len(progress))
	}

	// Every target is on the new batch: trees through stewardship, the feed's
	// updates as single-owner chunks, the lone chunk through /chunks.
	if len(n.Stamped) != 9 {
		t.Errorf("%d re-stamped, want 9: %v", len(n.Stamped), n.Stamped)
	}
	for addr, b := range n.Stamped {
		if b != fresh {
			t.Errorf("%s went to %s", addr, b)
		}
	}
	var socs, chunks, trees int
	for _, q := range stampingRequests(n) {
		switch {
		case strings.HasPrefix(q, "POST /soc/"):
			socs++
		case q == "POST /chunks":
			chunks++
		case strings.HasPrefix(q, "PUT /stewardship/"):
			trees++
		default:
			t.Errorf("unexpected %s", q)
		}
	}
	if socs != 3 || chunks != 1 || trees != 5 {
		t.Errorf("%d single-owner, %d content-addressed, %d trees; want 3, 1, 5", socs, chunks, trees)
	}
	for i := range 3 {
		a, _ := f.Address(uint64(i))
		if n.Stamped[a.String()] != fresh {
			t.Errorf("update %d not re-stamped", i)
		}
	}
	if lvl := n.Redundancy[hexOf(0xe5, 32)]; lvl != "2" {
		t.Errorf("the page went up with redundancy %q, want 2", lvl)
	}
	if lvl := n.Redundancy[hexOf(0xf6, 32)]; lvl != "0" {
		t.Errorf("the tree went up with redundancy %q, want 0 named", lvl)
	}

	// The lists name the new batch, the item's own batch is gone, and they
	// still read; the file keeps its mode.
	site := rot.Lists[0]
	if site.From[0] != long || site.From[1] != short || site.Trees != 4 || site.Chunks != 3 || site.Verified != 7 {
		t.Errorf("site: %+v", site)
	}
	for _, l := range ls {
		again, err := keeplist.Load(l.Path)
		if err != nil {
			t.Fatal(err)
		}
		if again.Batch != fresh || again.Items[len(again.Items)-1].Batch != "" {
			t.Errorf("%s: batch %s, items %+v", l.Name, again.Batch, again.Items)
		}
	}
	if again, _ := keeplist.Load(ls[0].Path); again.Items[1].Redundancy != 2 || again.Note != "why we keep it" || again.Items[1].Note != "the page" {
		t.Errorf("the rewritten list lost its redundancy or notes: %+v", again)
	}
	if fi, _ := os.Stat(ls[0].Path); fi.Mode().Perm() != 0o600 {
		t.Errorf("site.json mode %v", fi.Mode().Perm())
	}
	if m, _ := filepath.Glob(filepath.Join(filepath.Dir(ls[0].Path), ".swarm-keep-*")); len(m) != 0 {
		t.Errorf("left behind %v", m)
	}

	var text bytes.Buffer
	rot.Text(&text)
	for _, s := range []string{"Rotated onto batch " + fresh + " (depth 20, about 60 days left, fullest bucket 9 of 16, 0 before)",
		"site (from 01010101… 02020202…): 4 trees, 3 chunks: moved, 7 verified", "store (from 02020202…): 1 trees, 1 chunks: moved, 2 verified"} {
		if !strings.Contains(text.String(), s) {
			t.Errorf("the text lacks %q:\n%s", s, text.String())
		}
	}

	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	var entry keep.Rotation
	if err := json.Unmarshal(b, &entry); err != nil || entry.To != fresh || len(entry.Lists) != 2 || !entry.Lists[1].Moved {
		t.Errorf("log %v: %s", err, b)
	}
}

func TestRotateRefuses(t *testing.T) {
	n, c, ls, _ := rotation(t)
	n.OwnBatch(short, 5*day, beetest.Own{Usable: false, Immutable: true, Depth: 20, Room: -1})
	mutable := hexOf(0x77, 32)
	n.OwnBatch(mutable, 60*day, beetest.Own{Usable: true, Immutable: false, Depth: 20, Room: -1})
	for _, cs := range []struct{ to, want string }{
		{long, "is not this node's"},
		{short, "is not usable yet"},
		{mutable, "is mutable"},
	} {
		_, err := c.Rotate(context.Background(), ls, keep.RotateOptions{To: cs.to, Verify: keep.VerifyAll})
		if !errors.Is(err, keep.ErrRefused) || !strings.Contains(err.Error(), cs.want) {
			t.Errorf("onto %s: %v, want refused: %s", cs.to[:4], err, cs.want)
		}
	}

	// An item that cannot be listed refuses the whole rotation before
	// anything is stamped.
	broken := &keeplist.List{Keep: 1, Name: "broken", Items: []keeplist.Item{{Name: "missing file", List: "/nonexistent/refs.txt"}}}
	_, err := c.Rotate(context.Background(), append(ls, broken), keep.RotateOptions{To: fresh, Verify: keep.VerifyAll})
	if !errors.Is(err, keep.ErrRefused) || !strings.Contains(err.Error(), "broken, missing file") {
		t.Errorf("%v", err)
	}
	if q := stampingRequests(n); len(q) != 0 {
		t.Errorf("stamped while refusing: %v", q)
	}
}

func TestRotateDryRun(t *testing.T) {
	n, c, ls, _ := rotation(t)
	before, _ := os.ReadFile(ls[0].Path)
	rot, err := c.Rotate(context.Background(), ls, keep.RotateOptions{To: fresh, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if q := stampingRequests(n); len(q) != 0 {
		t.Errorf("a dry run stamped: %v", q)
	}
	if rot.OK() || rot.Lists[0].Trees != 4 || rot.Lists[1].Chunks != 1 {
		t.Errorf("%+v", rot)
	}
	if after, _ := os.ReadFile(ls[0].Path); !bytes.Equal(before, after) {
		t.Error("a dry run rewrote a list")
	}
	var text bytes.Buffer
	rot.Text(&text)
	if !strings.HasPrefix(text.String(), "Would rotate onto batch") {
		t.Errorf("%s", text.String())
	}
}

func TestRotateFailures(t *testing.T) {
	// A tree that fails keeps its list where it was; the other list moves.
	n, c, ls, _ := rotation(t)
	n.FailReupload[hexOf(0xe5, 32)] = true
	before, _ := os.ReadFile(ls[0].Path)
	rot, err := c.Rotate(context.Background(), ls, keep.RotateOptions{To: fresh, Verify: keep.VerifyAll, Workers: 2})
	if err != nil {
		t.Fatal(err)
	}
	site, store := rot.Lists[0], rot.Lists[1]
	if rot.OK() || site.Moved || len(site.Failed) != 1 || site.Failed[0].Item != "page" || !store.Moved {
		t.Errorf("%+v", rot)
	}
	if after, _ := os.ReadFile(ls[0].Path); !bytes.Equal(before, after) {
		t.Error("a list that did not move was rewritten")
	}

	// A target not retrievable afterwards fails its list too.
	n, c, ls, _ = rotation(t)
	n.Tree(hexOf(0xf6, 32), false)
	rot, err = c.Rotate(context.Background(), ls, keep.RotateOptions{To: fresh, Verify: keep.VerifyAll})
	if err != nil {
		t.Fatal(err)
	}
	if store := rot.Lists[1]; store.Moved || len(store.Failed) != 1 || !strings.Contains(store.Failed[0].Error, "not retrievable after re-stamping") {
		t.Errorf("%+v", store)
	}

	// A full batch stops everything, and no list is rewritten.
	n, c, ls, _ = rotation(t)
	n.OwnBatch(fresh, 60*day, beetest.Own{Usable: true, Immutable: true, Depth: 20, Room: 4})
	rot, err = c.Rotate(context.Background(), ls, keep.RotateOptions{To: fresh, Verify: keep.VerifyAll, Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	if rot.OK() || !strings.Contains(rot.Stopped, "is full") || rot.Lists[0].Moved || rot.Lists[1].Moved {
		t.Errorf("%+v", rot)
	}
	if len(n.Stamped) != 4 {
		t.Errorf("%d stamped after the batch filled, want 4", len(n.Stamped))
	}
	var text bytes.Buffer
	rot.Text(&text)
	if !strings.Contains(text.String(), "No list was rewritten") {
		t.Errorf("%s", text.String())
	}
}

func TestRotateStampsEachAddressOnce(t *testing.T) {
	// A third list keeps the store's tree and the site's page again, the
	// page without its redundancy: each is stamped once, at the highest
	// level any item names, and its outcome counts for every list.
	n, c, ls, _ := rotation(t)
	page, tree := hexOf(0xe5, 32), hexOf(0xf6, 32)
	copied := &keeplist.List{Keep: 1, Name: "copy", Items: []keeplist.Item{{Name: "tree", Ref: tree}, {Name: "page", Ref: page}}}
	ls = append(ls, copied)
	rot, err := c.Rotate(context.Background(), ls, keep.RotateOptions{To: fresh, Verify: keep.VerifyAll, Workers: 3})
	if err != nil {
		t.Fatal(err)
	}
	if !rot.OK() || rot.Addresses != 9 || rot.Lists[2].Verified != 2 {
		t.Errorf("%+v", rot)
	}
	seen := map[string]int{}
	for _, q := range n.Requests {
		seen[q]++
	}
	for _, r := range []string{page, tree} {
		if k := seen["PUT /stewardship/"+r]; k != 1 {
			t.Errorf("%s stamped %d times", r[:4], k)
		}
		if k := seen["GET /stewardship/"+r]; k != 1 {
			t.Errorf("%s verified %d times", r[:4], k)
		}
	}
	if lvl := n.Redundancy[page]; lvl != "2" {
		t.Errorf("the page went up with redundancy %q, want 2", lvl)
	}

	// A shared address that fails fails every list that keeps it.
	n, c, ls, _ = rotation(t)
	n.FailReupload[tree] = true
	rot, err = c.Rotate(context.Background(), append(ls, copied), keep.RotateOptions{To: fresh, Verify: keep.VerifyAll})
	if err != nil {
		t.Fatal(err)
	}
	if store, dup := rot.Lists[1], rot.Lists[2]; store.Moved || dup.Moved || len(dup.Failed) != 1 || !rot.Lists[0].Moved {
		t.Errorf("%+v", rot.Lists)
	}
}
