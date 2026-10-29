// Package keeplist reads keep-lists: JSON files that say what must stay on
// Swarm (docs/FORMAT.md). It checks a list completely before anything uses
// it, so a typo fails loudly instead of leaving data unkept.
package keeplist

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ethersphere/bee/v2/pkg/crypto"
)

// Version is the format version this package reads.
const Version = 1

// DefaultVersions is how many of a feed's latest versions are kept when a
// list does not say: the latest and one before it (Peter, 2026-10-29).
const DefaultVersions = 2

// DefaultWarn is how long before the estimated end a list warns when it
// does not say.
const DefaultWarn = 14 * 24 * time.Hour

// List is one keep-list.
type List struct {
	Keep     int       `json:"keep"`
	Name     string    `json:"name"`
	Note     string    `json:"note,omitempty"`
	Batch    string    `json:"batch,omitempty"`
	Versions *Versions `json:"versions,omitempty"`
	Warn     string    `json:"warn,omitempty"`
	Items    []Item    `json:"items"`

	// Path is the file the list was read from; relative paths in it are
	// read from its directory.
	Path string `json:"-"`
	// WarnAfter is Warn parsed, or DefaultWarn.
	WarnAfter time.Duration `json:"-"`
}

// Item is one thing to keep.
type Item struct {
	Name  string   `json:"name"`
	Ref   string   `json:"ref,omitempty"`
	Chunk string   `json:"chunk,omitempty"`
	Feed  *Feed    `json:"feed,omitempty"`
	List  string   `json:"list,omitempty"`
	Cmd   []string `json:"cmd,omitempty"`
	Walk  []string `json:"walk,omitempty"`
	// Redundancy is the erasure-coding level the item's trees were uploaded
	// with: 0 none, 1 medium, 2 strong, 3 insane, 4 paranoid.
	Redundancy int       `json:"redundancy,omitempty"`
	Batch      string    `json:"batch,omitempty"`
	Versions   *Versions `json:"versions,omitempty"`
	Note       string    `json:"note,omitempty"`
}

// Feed names a sequence feed.
type Feed struct {
	Owner       string `json:"owner"`
	Topic       string `json:"topic,omitempty"`
	TopicString string `json:"topic_string,omitempty"`
	Manifest    string `json:"manifest,omitempty"`
}

// Versions is how many of a feed's latest versions to keep; 0 means all.
type Versions int

// All keeps every version of a feed.
const All Versions = 0

// UnmarshalJSON reads a number from 1, or "all".
func (v *Versions) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		if s != "all" {
			return fmt.Errorf("versions %q: want a number from 1, or \"all\"", s)
		}
		*v = All
		return nil
	}
	var n int
	if err := json.Unmarshal(b, &n); err != nil || n < 1 {
		return fmt.Errorf("versions %s: want a number from 1, or \"all\"", b)
	}
	*v = Versions(n)
	return nil
}

// MarshalJSON writes "all" for All.
func (v Versions) MarshalJSON() ([]byte, error) {
	if v == All {
		return []byte(`"all"`), nil
	}
	return json.Marshal(int(v))
}

// Load reads and checks the keep-list at path.
func Load(path string) (*List, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	l, err := Parse(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	l.Path = path
	return l, nil
}

// LoadDir reads every *.json file in dir as a keep-list, in name order.
func LoadDir(dir string) ([]*List, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	var ls []*List
	for _, p := range paths {
		l, err := Load(p)
		if err != nil {
			return nil, err
		}
		ls = append(ls, l)
	}
	return ls, nil
}

// Parse reads and checks a keep-list.
func Parse(b []byte) (*List, error) {
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	var l List
	if err := d.Decode(&l); err != nil {
		return nil, err
	}
	if err := l.check(); err != nil {
		return nil, err
	}
	return &l, nil
}

func (l *List) check() error {
	switch {
	case l.Keep == 0:
		return errors.New(`"keep": missing; this format is version 1`)
	case l.Keep > Version:
		return fmt.Errorf(`"keep": %d is a newer format than this swarm-keep reads (%d)`, l.Keep, Version)
	case l.Keep != Version:
		return fmt.Errorf(`"keep": %d, want %d`, l.Keep, Version)
	case l.Name == "":
		return errors.New(`"name": missing`)
	case len(l.Items) == 0:
		return errors.New(`"items": the list keeps nothing`)
	}
	if l.Batch != "" {
		if err := hexOf("batch", l.Batch, 32); err != nil {
			return err
		}
	}
	l.WarnAfter = DefaultWarn
	if l.Warn != "" {
		d, err := ParseDuration(l.Warn)
		if err != nil {
			return fmt.Errorf(`"warn": %w`, err)
		}
		l.WarnAfter = d
	}
	names := map[string]bool{}
	for i := range l.Items {
		it := &l.Items[i]
		if err := it.check(); err != nil {
			if it.Name != "" {
				return fmt.Errorf("item %q: %w", it.Name, err)
			}
			return fmt.Errorf("item %d: %w", i+1, err)
		}
		if names[it.Name] {
			return fmt.Errorf("item %q: the name is used twice", it.Name)
		}
		names[it.Name] = true
	}
	return nil
}

func (it *Item) check() error {
	if it.Name == "" {
		return errors.New(`"name": missing`)
	}
	kinds := 0
	for _, set := range []bool{it.Ref != "", it.Chunk != "", it.Feed != nil, it.List != "", it.Cmd != nil} {
		if set {
			kinds++
		}
	}
	if kinds != 1 {
		return errors.New(`want exactly one of "ref", "chunk", "feed", "list" or "cmd"`)
	}
	switch {
	case it.Ref != "":
		if err := refOf("ref", it.Ref); err != nil {
			return err
		}
	case it.Chunk != "":
		if err := hexOf("chunk", it.Chunk, 32); err != nil {
			return err
		}
	case it.Feed != nil:
		if err := it.Feed.check(); err != nil {
			return err
		}
	case it.Cmd != nil:
		if len(it.Cmd) == 0 || it.Cmd[0] == "" {
			return errors.New(`"cmd": names no command`)
		}
	}
	if it.Walk != nil {
		if it.Ref == "" && it.Feed == nil {
			return errors.New(`"walk": only with "ref" or "feed"`)
		}
		if len(it.Walk) == 0 || it.Walk[0] == "" {
			return errors.New(`"walk": names no command`)
		}
		if !strings.Contains(strings.Join(it.Walk, "\x00"), "{ref}") {
			return errors.New(`"walk": no argument contains {ref}`)
		}
	}
	if it.Versions != nil && it.Feed == nil {
		return errors.New(`"versions": only with "feed"`)
	}
	switch {
	case it.Redundancy < 0 || it.Redundancy > 4:
		return fmt.Errorf(`"redundancy": %d, want 0 to 4`, it.Redundancy)
	case it.Redundancy > 0 && it.Chunk != "":
		return errors.New(`"redundancy": not with "chunk"`)
	}
	if it.Batch != "" {
		if err := hexOf("batch", it.Batch, 32); err != nil {
			return err
		}
	}
	return nil
}

func (f *Feed) check() error {
	if err := hexOf("feed owner", strings.TrimPrefix(f.Owner, "0x"), 20); err != nil {
		return err
	}
	switch {
	case f.Topic != "" && f.TopicString != "":
		return errors.New(`feed: "topic" and "topic_string" both given`)
	case f.Topic != "":
		if err := hexOf("feed topic", f.Topic, 32); err != nil {
			return err
		}
	case f.TopicString == "":
		return errors.New(`feed: "topic" or "topic_string" missing`)
	}
	if f.Manifest != "" {
		return refOf("feed manifest", f.Manifest)
	}
	return nil
}

// OwnerBytes is the feed owner's 20-byte address.
func (f *Feed) OwnerBytes() []byte {
	b, _ := hex.DecodeString(strings.TrimPrefix(f.Owner, "0x"))
	return b
}

// TopicBytes is the feed's 32-byte topic: Topic decoded, or TopicString
// hashed with Keccak-256 as bee-js does.
func (f *Feed) TopicBytes() []byte {
	if f.Topic != "" {
		b, _ := hex.DecodeString(f.Topic)
		return b
	}
	b, _ := crypto.LegacyKeccak256([]byte(f.TopicString))
	return b
}

// BatchOf is the batch that stamps it: its own, or the list's.
func (l *List) BatchOf(it *Item) string {
	if it.Batch != "" {
		return strings.ToLower(it.Batch)
	}
	return strings.ToLower(l.Batch)
}

// VersionsOf is how many of a feed item's latest versions to keep.
func (l *List) VersionsOf(it *Item) Versions {
	switch {
	case it.Versions != nil:
		return *it.Versions
	case l.Versions != nil:
		return *l.Versions
	}
	return DefaultVersions
}

// Resolve gives a path in the list relative to the list's directory.
func (l *List) Resolve(path string) string {
	if filepath.IsAbs(path) || l.Path == "" {
		return path
	}
	return filepath.Join(filepath.Dir(l.Path), path)
}

// ParseDuration reads a number with the unit h, d or wk, such as 36h, 14d or
// 2wk. A bare m or w is refused: m could be minutes or months.
func ParseDuration(s string) (time.Duration, error) {
	units := []struct {
		suffix string
		d      time.Duration
	}{{"wk", 7 * 24 * time.Hour}, {"h", time.Hour}, {"d", 24 * time.Hour}}
	for _, u := range units {
		if n, ok := strings.CutSuffix(s, u.suffix); ok {
			x, err := strconv.ParseFloat(n, 64)
			if err != nil || x < 0 {
				break
			}
			return time.Duration(x * float64(u.d)), nil
		}
	}
	return 0, fmt.Errorf("%q: want a number with h, d or wk, such as 36h, 14d or 2wk", s)
}

func hexOf(field, s string, n int) error {
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != n {
		return fmt.Errorf("%s %q: want %d hex digits", field, s, 2*n)
	}
	return nil
}

// refOf checks a Swarm reference: 32 bytes, or 64 for encrypted content.
func refOf(field, s string) error {
	b, err := hex.DecodeString(s)
	if err != nil || (len(b) != 32 && len(b) != 64) {
		return fmt.Errorf("%s %q: want 64 hex digits, or 128 for encrypted content", field, s)
	}
	return nil
}

// Save writes the list back to its file in swarm-keep's layout. It writes a
// new file beside the old one and renames it over, so a reader never sees
// half a list.
func (l *List) Save() error {
	b, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(l.Path); err == nil {
		mode = fi.Mode().Perm()
	}
	// Not *.json, which LoadDir would read if it were left behind.
	f, err := os.CreateTemp(filepath.Dir(l.Path), ".swarm-keep-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chmod(f.Name(), mode); err != nil {
		return err
	}
	return os.Rename(f.Name(), l.Path)
}
