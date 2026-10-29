package keep

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/ethersphere/bee/v2/pkg/swarm"

	"github.com/petfold/swarm-keep/internal/bee"
	"github.com/petfold/swarm-keep/internal/keeplist"
)

// Status is how an item stands.
type Status string

const (
	OK   Status = "ok"
	Warn Status = "warn" // needs attention: running short, or cannot tell
	Fail Status = "fail" // lost or about to be: expired, or not retrievable
)

func worse(a, b Status) Status {
	rank := map[Status]int{OK: 0, Warn: 1, Fail: 2}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

// Options say how thoroughly to check.
type Options struct {
	// Sample is how many of an item's targets to ask the network for, the
	// item's first target always among them. Deep asks for all of them.
	Sample int
	Deep   bool
	// Workers is how many requests run at once.
	Workers int
	// Rand picks the sample; nil uses a random seed.
	Rand *rand.Rand
}

// ItemReport is how one item stands.
type ItemReport struct {
	List  string `json:"list"`
	Item  string `json:"item"`
	Batch string `json:"batch,omitempty"`
	// SecondsLeft is the estimate at today's price; BalanceLeft the exact
	// balance per chunk, in PLUR, that it is computed from.
	SecondsLeft *int64   `json:"seconds_left,omitempty"`
	BalanceLeft string   `json:"balance_left_per_chunk,omitempty"`
	Targets     int      `json:"targets"`
	Checked     int      `json:"checked"`
	Missing     []string `json:"missing,omitempty"`
	Status      Status   `json:"status"`
	Problems    []string `json:"problems,omitempty"`
}

// TimeLeft is the estimate, or false when there is none.
func (r *ItemReport) TimeLeft() (time.Duration, bool) {
	if r.SecondsLeft == nil {
		return 0, false
	}
	return time.Duration(*r.SecondsLeft) * time.Second, true
}

func (r *ItemReport) problem(s Status, format string, a ...any) {
	r.Status = worse(r.Status, s)
	r.Problems = append(r.Problems, fmt.Sprintf(format, a...))
}

// Report is how everything kept stands.
type Report struct {
	At     time.Time    `json:"checked_at"`
	Block  uint64       `json:"block"`
	Price  string       `json:"price_per_chunk_per_block"`
	Status Status       `json:"status"`
	Items  []ItemReport `json:"items"`
}

// Checker checks keep-lists against one node.
type Checker struct {
	Bee    *bee.Client
	Expand *Expander

	state   bee.ChainState
	batches map[string]batchResult
}

type batchResult struct {
	b   bee.Batch
	err error
}

// Check reports on every item of every list.
func (c *Checker) Check(ctx context.Context, lists []*keeplist.List, o Options) (*Report, error) {
	st, err := c.Bee.ChainState(ctx)
	if err != nil {
		return nil, fmt.Errorf("the node's chain state: %w", err)
	}
	c.state = st
	c.batches = map[string]batchResult{}
	rep := &Report{At: time.Now().UTC(), Block: st.Block, Price: st.Price.String(), Status: OK}
	for _, l := range lists {
		for i := range l.Items {
			r := c.item(ctx, l, &l.Items[i], o)
			rep.Status = worse(rep.Status, r.Status)
			rep.Items = append(rep.Items, r)
		}
	}
	return rep, nil
}

func (c *Checker) item(ctx context.Context, l *keeplist.List, it *keeplist.Item, o Options) ItemReport {
	r := ItemReport{List: l.Name, Item: it.Name, Batch: l.BatchOf(it), Status: OK}
	c.batch(ctx, l, &r)
	ts, err := c.Expand.Expand(ctx, l, it)
	if err != nil {
		r.problem(Fail, "cannot list what it keeps: %v", err)
		return r
	}
	r.Targets = len(ts)
	c.retrievable(ctx, ts, o, &r)
	return r
}

// batch fills in how long the item's batch lasts.
func (c *Checker) batch(ctx context.Context, l *keeplist.List, r *ItemReport) {
	if r.Batch == "" {
		r.problem(Warn, "no batch recorded, so swarm-keep cannot tell how long it lasts")
		return
	}
	res, ok := c.batches[r.Batch]
	if !ok {
		res.b, res.err = c.Bee.Batch(ctx, r.Batch)
		c.batches[r.Batch] = res
	}
	switch {
	case errors.Is(res.err, bee.ErrNotFound):
		r.problem(Fail, "batch %s is not on chain: it expired, or never existed", short(r.Batch))
		return
	case res.err != nil:
		r.problem(Warn, "cannot read batch %s: %v", short(r.Batch), res.err)
		return
	}
	left := c.state.TimeLeft(res.b)
	secs := int64(left / time.Second)
	r.SecondsLeft = &secs
	r.BalanceLeft = c.state.Remaining(res.b).String()
	switch {
	case left <= 0:
		r.problem(Fail, "batch %s has run out", short(r.Batch))
	case left < l.WarnAfter:
		r.problem(Warn, "%s left on batch %s, under the list's warning time of %s", About(left), short(r.Batch), span(l.WarnAfter))
	}
}

// retrievable asks the network for a sample of the targets, or all of them.
func (c *Checker) retrievable(ctx context.Context, ts []Target, o Options, r *ItemReport) {
	pick := sample(len(ts), o)
	r.Checked = len(pick)
	workers := max(o.Workers, 1)
	type result struct {
		t       Target
		missing bool
		err     error
	}
	results := make([]result, len(pick))
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for k, i := range pick {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			missing, err := c.missing(ctx, ts[i])
			results[k] = result{ts[i], missing, err}
		}()
	}
	wg.Wait()
	var unknown int
	var firstErr error
	for _, res := range results {
		switch {
		case res.err != nil:
			unknown++
			if firstErr == nil {
				firstErr = res.err
			}
		case res.missing:
			r.Missing = append(r.Missing, res.t.Addr)
		}
	}
	if len(r.Missing) > 0 {
		r.problem(Fail, "%d of %d checked are not retrievable", len(r.Missing), r.Checked)
	}
	if unknown > 0 {
		r.problem(Warn, "%d of %d could not be checked: %v", unknown, r.Checked, firstErr)
	}
}

// missing asks the network whether a target is gone: a tree through the
// node's stewardship check, which retrieves every chunk from the network; a
// chunk by fetching it, which the node may answer from its own store.
func (c *Checker) missing(ctx context.Context, t Target) (bool, error) {
	if t.Kind == keeplist.Tree {
		ok, err := c.Bee.Retrievable(ctx, t.Addr)
		if errors.Is(err, bee.ErrNotFound) {
			return true, nil
		}
		return !ok, err
	}
	addr, err := swarm.ParseHexAddress(t.Addr)
	if err != nil {
		return false, err
	}
	_, err = c.Bee.Chunk(ctx, addr)
	if errors.Is(err, bee.ErrNotFound) {
		return true, nil
	}
	return false, err
}

// sample picks which of n targets to check: all with Deep or when there are
// few, else the first and Sample−1 others at random.
func sample(n int, o Options) []int {
	all := make([]int, n)
	for i := range all {
		all[i] = i
	}
	if o.Deep || n <= o.Sample {
		return all
	}
	if o.Sample <= 0 {
		return nil
	}
	rnd := o.Rand
	if rnd == nil {
		rnd = rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))
	}
	rest := all[1:]
	rnd.Shuffle(len(rest), func(i, j int) { rest[i], rest[j] = rest[j], rest[i] })
	return append([]int{0}, rest[:o.Sample-1]...)
}

// About writes an estimated duration as people say it: "about 20 hours",
// "about 10.5 days", "about 30 days".
func About(d time.Duration) string {
	if d <= 0 {
		return "nothing"
	}
	return "about " + span(d)
}

func span(d time.Duration) string {
	days := d.Hours() / 24
	switch {
	case d < 48*time.Hour:
		return fmt.Sprintf("%d hours", int(d.Hours()+0.5))
	case days < 14 && days != float64(int(days)):
		return fmt.Sprintf("%.1f days", days)
	}
	return fmt.Sprintf("%d days", int(days+0.5))
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8] + "…"
	}
	return id
}

// plur is a balance with its unit, for reports.
func plur(s string) string {
	n, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return s
	}
	return n.String() + " PLUR"
}
