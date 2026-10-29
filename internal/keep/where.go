package keep

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/petfold/swarm-keep/internal/keeplist"
)

// Place is where a reference is kept: an item, and how long its batch lasts.
type Place struct {
	ItemReport
	Kind keeplist.Kind `json:"kind"`
	Why  string        `json:"why,omitempty"`
}

// Whereabouts is what is known about a reference.
type Whereabouts struct {
	Ref   string  `json:"ref"`
	Kept  []Place `json:"kept"`
	Found bool    `json:"retrievable"`
	// Unchecked says why retrievability is unknown, when it is.
	Unchecked string `json:"unchecked,omitempty"`
}

// Where finds the items that keep ref, with their batches and time left,
// and asks the network whether ref is retrievable.
func (c *Checker) Where(ctx context.Context, lists []*keeplist.List, ref string) (*Whereabouts, error) {
	ref = strings.ToLower(ref)
	st, err := c.Bee.ChainState(ctx)
	if err != nil {
		return nil, fmt.Errorf("the node's chain state: %w", err)
	}
	c.state = st
	c.batches = map[string]batchResult{}
	w := &Whereabouts{Ref: ref}
	kind := keeplist.Tree
	for _, l := range lists {
		for i := range l.Items {
			it := &l.Items[i]
			ts, err := c.Expand.Expand(ctx, l, it)
			if err != nil {
				return nil, fmt.Errorf("%s, %s: %w", l.Name, it.Name, err)
			}
			for _, t := range ts {
				if t.Addr != ref {
					continue
				}
				p := Place{ItemReport: ItemReport{List: l.Name, Item: it.Name, Batch: l.BatchOf(it), Status: OK}, Kind: t.Kind, Why: t.Why}
				c.batch(ctx, l, &p.ItemReport)
				w.Kept = append(w.Kept, p)
				kind = t.Kind
				break
			}
		}
	}
	missing, err := c.missing(ctx, Target{Entry: keeplist.Entry{Kind: kind, Addr: ref}})
	if err != nil {
		w.Unchecked = err.Error()
	}
	w.Found = err == nil && !missing
	return w, nil
}

// Text writes the whereabouts for people.
func (w *Whereabouts) Text(out io.Writer) {
	switch {
	case w.Unchecked != "":
		fmt.Fprintf(out, "%s: could not ask the network: %s\n", w.Ref, w.Unchecked)
	case w.Found:
		fmt.Fprintf(out, "%s: retrievable now\n", w.Ref)
	default:
		fmt.Fprintf(out, "%s: NOT retrievable now\n", w.Ref)
	}
	if len(w.Kept) == 0 {
		fmt.Fprintln(out, "No keep-list keeps it, so nothing here records its batch. To keep it, add it to a keep-list and rotate it onto your own batch.")
		return
	}
	for _, p := range w.Kept {
		left := "time unknown"
		if d, ok := p.TimeLeft(); ok {
			left = About(d) + " left"
		}
		where := p.Item
		if p.Why != "" {
			where += ", " + p.Why
		}
		batch := "no batch recorded"
		if p.Batch != "" {
			batch = "batch " + p.Batch
		}
		fmt.Fprintf(out, "  kept by %s: %s\n    %s, %s\n", p.List, where, batch, left)
		if p.BalanceLeft != "" {
			fmt.Fprintf(out, "    %s per chunk left (the time is an estimate at today's price)\n", plur(p.BalanceLeft))
		}
		for _, pr := range p.Problems {
			fmt.Fprintf(out, "    %s\n", pr)
		}
	}
}
