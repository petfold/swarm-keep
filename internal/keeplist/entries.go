package keeplist

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// Kind is how an entry is kept.
type Kind int

const (
	// Tree is a reference whose whole tree the node walks: a file, a
	// folder or site, raw bytes.
	Tree Kind = iota
	// Chunk is one chunk, content-addressed or single-owner, kept as it is.
	Chunk
)

func (k Kind) String() string {
	if k == Chunk {
		return "chunk"
	}
	return "ref"
}

// Entry is one reference or chunk to keep, as a list, a command or a walk
// prints it.
type Entry struct {
	Kind Kind
	Addr string // lower-case hex
}

// ParseEntries reads entries, one per line: a reference (bare or after
// "ref"), or "chunk" and a chunk address. Empty lines and lines starting
// with # are skipped.
func ParseEntries(r io.Reader) ([]Entry, error) {
	var es []Entry
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 64*1024), 1024*1024)
	for n := 1; s.Scan(); n++ {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		e, err := parseEntry(line)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", n, err)
		}
		es = append(es, e)
	}
	return es, s.Err()
}

func parseEntry(line string) (Entry, error) {
	f := strings.Fields(line)
	switch {
	case len(f) == 1:
		return Entry{Tree, strings.ToLower(f[0])}, refOf("reference", f[0])
	case len(f) == 2 && f[0] == "ref":
		return Entry{Tree, strings.ToLower(f[1])}, refOf("reference", f[1])
	case len(f) == 2 && f[0] == "chunk":
		return Entry{Chunk, strings.ToLower(f[1])}, hexOf("chunk", f[1], 32)
	}
	return Entry{}, fmt.Errorf("%q: want a reference, \"ref <hex>\" or \"chunk <hex>\"", line)
}
