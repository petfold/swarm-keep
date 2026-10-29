package keeplist

import (
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/ethersphere/bee/v2/pkg/crypto"
)

const (
	ref   = "2abb4979d03acd202fe547cdb1902b86b06bd9ce16c341cd9cfe4d9a2e6b5442"
	batch = "c931c8a5ee8def225abf86a934bbf38ab191f10456f1a684c9cb44f534834359"
	owner = "0xe2bfb7570000000000000000000000000000beef"
)

func TestParseExample(t *testing.T) {
	l, err := Parse([]byte(`{
	  "keep": 1, "name": "loopmarket", "batch": "` + batch + `", "versions": 3, "warn": "2wk",
	  "items": [
	    {"name": "page", "ref": "` + ref + `"},
	    {"name": "feed", "feed": {"owner": "` + owner + `", "topic_string": "loop-e2bfb757", "manifest": "` + ref + `"},
	     "walk": ["recordstore", "refs", "{ref}"], "versions": "all"},
	    {"name": "chunk", "chunk": "` + ref + `", "batch": "` + strings.ToUpper(batch) + `"},
	    {"name": "list", "list": "refs.txt"},
	    {"name": "cmd", "cmd": ["echo", "` + ref + `"], "note": "anything"}
	  ]}`))
	if err != nil {
		t.Fatal(err)
	}
	if l.WarnAfter != 14*24*time.Hour {
		t.Errorf("WarnAfter %v", l.WarnAfter)
	}
	feed, chunk, page := &l.Items[1], &l.Items[2], &l.Items[0]
	if got := l.VersionsOf(page); got != 3 {
		t.Errorf("the list's versions: %d", got)
	}
	if got := l.VersionsOf(feed); got != All {
		t.Errorf("the item's versions: %d", got)
	}
	if got := l.BatchOf(chunk); got != batch {
		t.Errorf("the item's batch, lower case: %s", got)
	}
	want, _ := crypto.LegacyKeccak256([]byte("loop-e2bfb757"))
	if got := feed.Feed.TopicBytes(); hex.EncodeToString(got) != hex.EncodeToString(want) {
		t.Errorf("topic %x, want %x", got, want)
	}
	if got := feed.Feed.OwnerBytes(); len(got) != 20 || got[0] != 0xe2 {
		t.Errorf("owner %x", got)
	}
}

func TestDefaults(t *testing.T) {
	l, err := Parse([]byte(`{"keep": 1, "name": "x", "items": [{"name": "f", "feed": {"owner": "` + owner[2:] + `", "topic": "` + ref + `"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if l.WarnAfter != DefaultWarn || l.VersionsOf(&l.Items[0]) != DefaultVersions {
		t.Errorf("warn %v, versions %d", l.WarnAfter, l.VersionsOf(&l.Items[0]))
	}
}

func TestParseRefuses(t *testing.T) {
	item := func(s string) string { return `{"keep": 1, "name": "x", "items": [` + s + `]}` }
	for _, c := range []struct{ json, want string }{
		{`{"name": "x", "items": [{"name": "a", "ref": "` + ref + `"}]}`, `"keep": missing`},
		{`{"keep": 2, "name": "x", "items": [{"name": "a", "ref": "` + ref + `"}]}`, "newer format"},
		{`{"keep": 1, "items": [{"name": "a", "ref": "` + ref + `"}]}`, `"name": missing`},
		{`{"keep": 1, "name": "x", "items": []}`, "keeps nothing"},
		{`{"keep": 1, "name": "x", "bach": "` + batch + `", "items": [{"name": "a", "ref": "` + ref + `"}]}`, `unknown field "bach"`},
		{`{"keep": 1, "name": "x", "batch": "c931", "items": [{"name": "a", "ref": "` + ref + `"}]}`, "want 64 hex digits"},
		{`{"keep": 1, "name": "x", "warn": "2w", "items": [{"name": "a", "ref": "` + ref + `"}]}`, "h, d or wk"},
		{`{"keep": 1, "name": "x", "versions": 0, "items": [{"name": "a", "ref": "` + ref + `"}]}`, "a number from 1"},
		{item(`{"name": "a"}`), "exactly one of"},
		{item(`{"name": "a", "ref": "` + ref + `", "chunk": "` + ref + `"}`), "exactly one of"},
		{item(`{"ref": "` + ref + `"}`), `"name": missing`},
		{item(`{"name": "a", "ref": "xyz"}`), "128 for encrypted"},
		{item(`{"name": "a", "ref": "` + ref + `"}, {"name": "a", "ref": "` + ref + `"}`), "used twice"},
		{item(`{"name": "a", "chunk": "` + ref + `", "walk": ["w", "{ref}"]}`), `only with "ref" or "feed"`},
		{item(`{"name": "a", "ref": "` + ref + `", "walk": ["w", "ref"]}`), "contains {ref}"},
		{item(`{"name": "a", "ref": "` + ref + `", "versions": 2}`), `only with "feed"`},
		{item(`{"name": "a", "cmd": []}`), "names no command"},
		{item(`{"name": "a", "feed": {"owner": "` + owner + `"}}`), "topic"},
		{item(`{"name": "a", "feed": {"owner": "` + owner + `", "topic": "` + ref + `", "topic_string": "t"}}`), "both given"},
		{item(`{"name": "a", "feed": {"owner": "0x12", "topic": "` + ref + `"}}`), "feed owner"},
	} {
		_, err := Parse([]byte(c.json))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s\n  error %v, want one saying %q", c.json, err, c.want)
		}
	}
}

func TestParseDuration(t *testing.T) {
	for s, want := range map[string]time.Duration{"36h": 36 * time.Hour, "14d": 14 * 24 * time.Hour, "2wk": 14 * 24 * time.Hour, "1.5d": 36 * time.Hour} {
		if got, err := ParseDuration(s); err != nil || got != want {
			t.Errorf("%s: %v %v, want %v", s, got, err, want)
		}
	}
	for _, s := range []string{"", "14", "2w", "3m", "d", "-1d", "1x"} {
		if _, err := ParseDuration(s); err == nil {
			t.Errorf("%q: no error", s)
		}
	}
}

func TestParseEntries(t *testing.T) {
	es, err := ParseEntries(strings.NewReader("# a comment\n\n" + ref + "\nref " + strings.ToUpper(ref) + "\n  chunk " + ref + "  \n" + ref + ref + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := []Entry{{Tree, ref}, {Tree, ref}, {Chunk, ref}, {Tree, ref + ref}}
	if len(es) != len(want) {
		t.Fatalf("%v, want %v", es, want)
	}
	for i := range want {
		if es[i] != want[i] {
			t.Errorf("entry %d: %v, want %v", i, es[i], want[i])
		}
	}
	for _, bad := range []string{"chunk " + ref + ref, "ref", "file " + ref, "c931"} {
		if _, err := ParseEntries(strings.NewReader(ref + "\n" + bad + "\n")); err == nil || !strings.Contains(err.Error(), "line 2") {
			t.Errorf("%q: error %v, want one naming line 2", bad, err)
		}
	}
}
