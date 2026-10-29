package keep

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ethersphere/bee/v2/pkg/swarm"

	"github.com/petfold/swarm-keep/internal/bee"
	"github.com/petfold/swarm-keep/internal/keeplist"
)

// Verify says how much of a rotation to ask the network for afterwards.
type Verify string

const (
	VerifyAll    Verify = "all"
	VerifySample Verify = "sample"
	VerifyNone   Verify = "none"
)

// RotateOptions say where to and how.
type RotateOptions struct {
	To      string // the batch to move onto: the node's own, usable, immutable
	DryRun  bool   // list what would move, and stop
	Verify  Verify
	Sample  int // with VerifySample, as Options.Sample
	Workers int
	// Progress, when set, is called as targets are re-stamped, one call at
	// a time.
	Progress func(done, total, failed int)
	// Log is where each rotation is appended as a JSON line; empty for none.
	Log string
}

// Failure is a target that did not move or did not verify.
type Failure struct {
	Item  string `json:"item"`
	Addr  string `json:"addr"`
	Error string `json:"error"`
}

// ListRotation is what happened to one list.
type ListRotation struct {
	List     string    `json:"list"`
	Path     string    `json:"path"`
	From     []string  `json:"from"` // the batches it was on
	Trees    int       `json:"trees"`
	Chunks   int       `json:"chunks"`
	Verified int       `json:"verified"`
	Failed   []Failure `json:"failed,omitempty"`
	// Moved is true once the list's file names the new batch: only when
	// every target moved and every one asked for verified.
	Moved bool `json:"moved"`
}

// Rotation is what a rotation did.
type Rotation struct {
	At     time.Time      `json:"at"`
	To     string         `json:"to"`
	DryRun bool           `json:"dry_run,omitempty"`
	Batch  bee.OwnBatch   `json:"batch"`           // before stamping
	After  *bee.OwnBatch  `json:"after,omitempty"` // after stamping
	Lists  []ListRotation `json:"lists"`
	// Stopped says why stamping stopped early, such as a full batch.
	Stopped string `json:"stopped,omitempty"`
}

// OK is true when every list moved.
func (r *Rotation) OK() bool {
	if r.DryRun || r.Stopped != "" {
		return false
	}
	for _, l := range r.Lists {
		if !l.Moved {
			return false
		}
	}
	return true
}

// ErrRefused means that the rotation was refused before anything was
// stamped.
var ErrRefused = errors.New("refused")

// Rotate re-stamps everything the lists keep onto o.To, verifies it, and
// rewrites each list whose targets all moved to name the new batch. Before
// stamping anything it checks the batch and lists every item, so a batch
// that cannot take stamps, or an item that cannot be listed, changes
// nothing. Running it again after a failure is safe: a chunk stamped again
// on the same batch keeps its slot.
func (c *Checker) Rotate(ctx context.Context, lists []*keeplist.List, o RotateOptions) (*Rotation, error) {
	b, err := c.Bee.OwnBatch(ctx, o.To)
	switch {
	case errors.Is(err, bee.ErrNotFound):
		return nil, fmt.Errorf("%w: batch %s is not this node's; only the node that bought a batch can stamp with it", ErrRefused, o.To)
	case err != nil:
		return nil, err
	case !b.Exists || !b.Usable:
		return nil, fmt.Errorf("%w: batch %s is not usable yet; a new batch takes a minute or two", ErrRefused, short(o.To))
	case !b.Immutable:
		return nil, fmt.Errorf("%w: batch %s is mutable, and a full mutable batch overwrites its oldest chunks; keep data on immutable batches", ErrRefused, short(o.To))
	}
	rot := &Rotation{At: time.Now().UTC(), To: o.To, DryRun: o.DryRun, Batch: b}

	type job struct {
		list int
		item string
		t    Target
	}
	var jobs []job
	for li, l := range lists {
		lr := ListRotation{List: l.Name, Path: l.Path}
		from := map[string]bool{}
		for i := range l.Items {
			it := &l.Items[i]
			if b := l.BatchOf(it); b != "" {
				from[b] = true
			}
			ts, err := c.Expand.Expand(ctx, l, it)
			if err != nil {
				return nil, fmt.Errorf("%w: %s, %s: cannot list what it keeps: %v", ErrRefused, l.Name, it.Name, err)
			}
			for _, t := range ts {
				if t.Kind == keeplist.Tree {
					lr.Trees++
				} else {
					lr.Chunks++
				}
				jobs = append(jobs, job{li, it.Name, t})
			}
		}
		for b := range from {
			lr.From = append(lr.From, b)
		}
		sort.Strings(lr.From)
		rot.Lists = append(rot.Lists, lr)
	}
	if o.DryRun {
		return rot, nil
	}

	// Stamp. A full bucket stops everything: the rest would fail too.
	stampCtx, stop := context.WithCancel(ctx)
	defer stop()
	var mu, progressMu sync.Mutex
	var done, failed atomic.Int64
	fail := func(j job, err error) {
		mu.Lock()
		defer mu.Unlock()
		lr := &rot.Lists[j.list]
		lr.Failed = append(lr.Failed, Failure{Item: j.item, Addr: j.t.Addr, Error: err.Error()})
		if errors.Is(err, bee.ErrOverissued) && rot.Stopped == "" {
			rot.Stopped = fmt.Sprintf("batch %s is full: a bucket took no more chunks", short(o.To))
			stop()
		}
	}
	each(stampCtx, len(jobs), o.Workers, func(i int) {
		j := jobs[i]
		if err := c.restamp(stampCtx, j.t, o.To); err != nil {
			failed.Add(1)
			fail(j, err)
		}
		if o.Progress == nil {
			done.Add(1)
			return
		}
		progressMu.Lock()
		o.Progress(int(done.Add(1)), len(jobs), int(failed.Load()))
		progressMu.Unlock()
	})
	if rot.Stopped == "" && ctx.Err() != nil {
		rot.Stopped = ctx.Err().Error()
	}
	if a, err := c.Bee.OwnBatch(ctx, o.To); err == nil {
		rot.After = &a
	}

	// Verify what moved.
	if rot.Stopped == "" && o.Verify != VerifyNone {
		var picks []int
		for li := range rot.Lists {
			var mine []int
			for i, j := range jobs {
				if j.list == li {
					mine = append(mine, i)
				}
			}
			s := Options{Sample: o.Sample, Deep: o.Verify == VerifyAll}
			for _, k := range sample(len(mine), s) {
				picks = append(picks, mine[k])
			}
		}
		var verified sync.Map
		each(ctx, len(picks), o.Workers, func(k int) {
			j := jobs[picks[k]]
			missing, err := c.missing(ctx, j.t)
			switch {
			case err != nil:
				fail(j, fmt.Errorf("could not verify: %w", err))
			case missing:
				fail(j, errors.New("not retrievable after re-stamping"))
			default:
				verified.Store(picks[k], j.list)
			}
		})
		verified.Range(func(_, li any) bool {
			rot.Lists[li.(int)].Verified++
			return true
		})
	}

	// Record: a list names the new batch only when all of it moved.
	for li, l := range lists {
		lr := &rot.Lists[li]
		if rot.Stopped != "" || len(lr.Failed) > 0 {
			continue
		}
		l.Batch = o.To
		for i := range l.Items {
			l.Items[i].Batch = ""
		}
		if l.Path != "" {
			if err := l.Save(); err != nil {
				lr.Failed = append(lr.Failed, Failure{Error: "everything moved, but the list could not be rewritten: " + err.Error()})
				continue
			}
		}
		lr.Moved = true
	}
	if o.Log != "" {
		if err := appendLog(o.Log, rot); err != nil {
			return rot, fmt.Errorf("the rotation is done, but its log entry could not be written: %w", err)
		}
	}
	return rot, nil
}

// restamp stamps a target again onto batch: a tree through the node's
// stewardship, which re-stamps every chunk under it; a chunk by fetching it
// and posting it again.
func (c *Checker) restamp(ctx context.Context, t Target, batch string) error {
	if t.Kind == keeplist.Tree {
		return c.Bee.Reupload(ctx, t.Addr, batch, t.Redundancy)
	}
	addr, err := swarm.ParseHexAddress(t.Addr)
	if err != nil {
		return err
	}
	ch, err := c.Bee.Chunk(ctx, addr)
	if err != nil {
		return err
	}
	return c.Bee.PutChunk(ctx, batch, ch)
}

// each runs f(0) … f(n−1) on up to workers goroutines, and stops starting
// new ones once ctx is done.
func each(ctx context.Context, n, workers int, f func(i int)) {
	sem := make(chan struct{}, max(workers, 1))
	var wg sync.WaitGroup
	for i := range n {
		if ctx.Err() != nil {
			break
		}
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			f(i)
		}()
	}
	wg.Wait()
}

func appendLog(path string, rot *Rotation) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	b, err := json.Marshal(rot)
	if err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Text writes the rotation for people.
func (r *Rotation) Text(w io.Writer) {
	left := About(time.Duration(r.Batch.TTL) * time.Second)
	verb := "Rotated"
	if r.DryRun {
		verb = "Would rotate"
	}
	fill := fmt.Sprintf("fullest bucket %d of %d", r.Batch.Utilization, r.Batch.BucketCapacity())
	if r.After != nil {
		fill = fmt.Sprintf("fullest bucket %d of %d, %d before", r.After.Utilization, r.After.BucketCapacity(), r.Batch.Utilization)
	}
	fmt.Fprintf(w, "%s onto batch %s (depth %d, %s left, %s)\n", verb, r.To, r.Batch.Depth, left, fill)
	for _, l := range r.Lists {
		from := "no batch recorded"
		if len(l.From) > 0 {
			from = "from"
			for _, b := range l.From {
				from += " " + short(b)
			}
		}
		state := ""
		switch {
		case r.DryRun:
		case l.Moved:
			state = fmt.Sprintf(": moved, %d verified", l.Verified)
		default:
			state = fmt.Sprintf(": NOT moved, %d failed", len(l.Failed))
		}
		fmt.Fprintf(w, "  %s (%s): %d trees, %d chunks%s\n", l.List, from, l.Trees, l.Chunks, state)
		for i, f := range l.Failed {
			if i == 10 {
				fmt.Fprintf(w, "        … and %d more\n", len(l.Failed)-10)
				break
			}
			if f.Addr == "" {
				fmt.Fprintf(w, "        %s\n", f.Error)
				continue
			}
			fmt.Fprintf(w, "        %s, %s: %s\n", f.Item, f.Addr, f.Error)
		}
	}
	if r.Stopped != "" {
		fmt.Fprintf(w, "Stopped: %s. No list was rewritten.\n", r.Stopped)
	}
}
