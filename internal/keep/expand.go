// Package keep turns keep-list items into the trees and chunks they keep,
// and checks how long those have left and whether the network has them.
package keep

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethersphere/bee/v2/pkg/swarm"

	"github.com/petfold/swarm-keep/internal/bee"
	"github.com/petfold/swarm-keep/internal/feed"
	"github.com/petfold/swarm-keep/internal/keeplist"
)

// Target is one tree or chunk an item keeps.
type Target struct {
	keeplist.Entry
	Why string // where it comes from, such as "update 3" or "version 2 of 2"
}

// Expander turns items into targets.
type Expander struct {
	Bee *bee.Client
	// Run runs a command (cmd, walk) and returns its standard output. Nil
	// runs it with os/exec.
	Run func(ctx context.Context, argv []string, dir string) ([]byte, error)
}

// Expand returns the targets of item it in list l, each once, in the order
// found.
func (x *Expander) Expand(ctx context.Context, l *keeplist.List, it *keeplist.Item) ([]Target, error) {
	var ts targets
	switch {
	case it.Ref != "":
		ts.add(keeplist.Tree, it.Ref, "")
		if err := x.walk(ctx, l, it, it.Ref, "", &ts); err != nil {
			return nil, err
		}
	case it.Chunk != "":
		ts.add(keeplist.Chunk, it.Chunk, "")
	case it.List != "":
		f, err := os.Open(l.Resolve(it.List))
		if err != nil {
			return nil, err
		}
		defer f.Close()
		es, err := keeplist.ParseEntries(f)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", it.List, err)
		}
		ts.entries(es, "")
	case it.Cmd != nil:
		es, err := x.command(ctx, l, it.Cmd, "")
		if err != nil {
			return nil, err
		}
		ts.entries(es, "")
	case it.Feed != nil:
		if err := x.feed(ctx, l, it, &ts); err != nil {
			return nil, err
		}
	}
	return ts.list, nil
}

func (x *Expander) feed(ctx context.Context, l *keeplist.List, it *keeplist.Item, ts *targets) error {
	f := feed.Feed{Owner: common.BytesToAddress(it.Feed.OwnerBytes()), Topic: it.Feed.TopicBytes()}
	if it.Feed.Manifest != "" {
		ts.add(keeplist.Tree, it.Feed.Manifest, "feed manifest")
	}
	n, err := feed.Latest(ctx, x.Bee, f)
	if err != nil {
		return fmt.Errorf("feed: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("feed 0x%x/%x has no updates", f.Owner[:], f.Topic)
	}
	keep := uint64(l.VersionsOf(it))
	if keep == 0 || keep > n {
		keep = n
	}
	for i := uint64(0); i < n; i++ {
		addr, err := f.Address(i)
		if err != nil {
			return err
		}
		ts.add(keeplist.Chunk, addr.String(), fmt.Sprintf("update %d", i))
		if i < n-keep {
			continue
		}
		why := fmt.Sprintf("version %d of %d", i+1, n)
		ch, err := x.Bee.Chunk(ctx, addr)
		if err != nil {
			return fmt.Errorf("feed update %d: %w", i, err)
		}
		ref, span, err := feed.Content(ch)
		if err != nil {
			return err
		}
		if ref == nil {
			// The content travels inside the update. Past one chunk its
			// children would need keeping too, which needs a walk from a
			// root that is not stored at its own address: not built.
			if span > swarm.ChunkSize {
				return fmt.Errorf("feed update %d carries %d bytes of content inside itself, more than one chunk: not supported yet", i, span)
			}
			continue
		}
		r := hex.EncodeToString(ref)
		ts.add(keeplist.Tree, r, why)
		if err := x.walk(ctx, l, it, r, why, ts); err != nil {
			return err
		}
	}
	return nil
}

func (x *Expander) walk(ctx context.Context, l *keeplist.List, it *keeplist.Item, ref, why string, ts *targets) error {
	if it.Walk == nil {
		return nil
	}
	argv := make([]string, len(it.Walk))
	for i, a := range it.Walk {
		argv[i] = strings.ReplaceAll(a, "{ref}", ref)
	}
	es, err := x.command(ctx, l, argv, why)
	if err != nil {
		return err
	}
	if why == "" {
		why = "walk"
	} else {
		why += ", walk"
	}
	ts.entries(es, why)
	return nil
}

func (x *Expander) command(ctx context.Context, l *keeplist.List, argv []string, why string) ([]keeplist.Entry, error) {
	run := x.Run
	if run == nil {
		run = runCommand
	}
	dir := l.Resolve(".")
	out, err := run(ctx, argv, dir)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", strings.Join(argv, " "), err)
	}
	es, err := keeplist.ParseEntries(bytes.NewReader(out))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", strings.Join(argv, " "), err)
	}
	return es, nil
}

func runCommand(ctx context.Context, argv []string, dir string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("%w: %s", err, msg)
		}
		return nil, err
	}
	return out, nil
}

// targets collects targets, each once.
type targets struct {
	list []Target
	seen map[keeplist.Entry]bool
}

func (ts *targets) add(k keeplist.Kind, addr, why string) {
	e := keeplist.Entry{Kind: k, Addr: strings.ToLower(addr)}
	if ts.seen == nil {
		ts.seen = map[keeplist.Entry]bool{}
	}
	if ts.seen[e] {
		return
	}
	ts.seen[e] = true
	ts.list = append(ts.list, Target{Entry: e, Why: why})
}

func (ts *targets) entries(es []keeplist.Entry, why string) {
	for _, e := range es {
		ts.add(e.Kind, e.Addr, why)
	}
}
