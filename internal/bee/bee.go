// Package bee is a small client for the HTTP API that Bee serves, and Ant
// with it: the calls swarm-keep needs to find, check and re-stamp content.
package bee

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/ethersphere/bee/v2/pkg/cac"
	"github.com/ethersphere/bee/v2/pkg/soc"
	"github.com/ethersphere/bee/v2/pkg/swarm"
)

// BlockTime is Gnosis Chain's block time, which postage prices are paid per.
const BlockTime = 5 * time.Second

// ErrNotFound means that the node answered 404.
var ErrNotFound = errors.New("not found")

// ErrInvalidChunk means that a node returned data that does not hash to the
// address asked for.
var ErrInvalidChunk = errors.New("chunk data does not match its address")

// Error is an error response from the node.
type Error struct {
	Op      string // the request, such as "GET /stamps/…"
	Status  int    // the HTTP status
	Message string // the node's message
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %d %s: %s", e.Op, e.Status, http.StatusText(e.Status), e.Message)
}

// Is reports a 404 as ErrNotFound.
func (e *Error) Is(target error) bool {
	return target == ErrNotFound && e.Status == http.StatusNotFound
}

// Client sends requests to one node.
type Client struct {
	URL  string       // API base, such as http://localhost:1633
	HTTP *http.Client // nil means http.DefaultClient
}

// New returns a client for the node API at url that keeps up to conns
// connections open.
func New(url string, conns int) *Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil // talk to the node directly, whatever HTTP_PROXY says
	tr.MaxIdleConnsPerHost = conns
	return &Client{URL: strings.TrimSuffix(url, "/"), HTTP: &http.Client{Transport: tr}}
}

// do sends a request and returns the response if its status is want. Any
// other status becomes an *Error carrying the node's message.
func (c *Client) do(req *http.Request, want int) (*http.Response, error) {
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == want {
		return resp, nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	msg := strings.TrimSpace(string(body))
	var e struct{ Message string }
	if json.Unmarshal(body, &e) == nil && e.Message != "" {
		msg = e.Message
	}
	return nil, &Error{Op: req.Method + " " + req.URL.Path, Status: resp.StatusCode, Message: msg}
}

func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.do(req, http.StatusOK)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(out)
}

// Batch is a postage batch as GET /batches/{id} reports it: any batch on
// chain, not only the node's own.
type Batch struct {
	ID        string
	Owner     string
	Depth     uint8
	Immutable bool
	// Value is the batch's balance per chunk, counted from the start of the
	// chain's postage: what remains is Value minus the chain's TotalAmount.
	Value *big.Int
	// TTL is the node's estimate, in seconds, at the current price.
	TTL int64
}

// Batch returns the batch with the hex ID id, or an error that Is
// ErrNotFound when the chain has no such batch (or it expired).
func (c *Client) Batch(ctx context.Context, id string) (Batch, error) {
	var r struct {
		ID        string `json:"batchID"`
		Value     string `json:"value"`
		Owner     string `json:"owner"`
		Depth     uint8  `json:"depth"`
		Immutable bool   `json:"immutable"`
		TTL       int64  `json:"batchTTL"`
	}
	if err := c.getJSON(ctx, "/batches/"+id, &r); err != nil {
		return Batch{}, err
	}
	v, ok := new(big.Int).SetString(r.Value, 10)
	if !ok {
		return Batch{}, fmt.Errorf("GET /batches/%s: value %q", id, r.Value)
	}
	return Batch{ID: r.ID, Owner: r.Owner, Depth: r.Depth, Immutable: r.Immutable, Value: v, TTL: r.TTL}, nil
}

// ChainState is the chain's postage state as the node sees it.
type ChainState struct {
	Block       uint64
	TotalAmount *big.Int // what the network has drawn per chunk so far
	Price       *big.Int // PLUR per chunk per block, now
}

// ChainState returns GET /chainstate.
func (c *Client) ChainState(ctx context.Context) (ChainState, error) {
	var r struct {
		Block       uint64 `json:"block"`
		TotalAmount string `json:"totalAmount"`
		Price       string `json:"currentPrice"`
	}
	if err := c.getJSON(ctx, "/chainstate", &r); err != nil {
		return ChainState{}, err
	}
	t, ok1 := new(big.Int).SetString(r.TotalAmount, 10)
	p, ok2 := new(big.Int).SetString(r.Price, 10)
	if !ok1 || !ok2 {
		return ChainState{}, fmt.Errorf("GET /chainstate: totalAmount %q, currentPrice %q", r.TotalAmount, r.Price)
	}
	return ChainState{Block: r.Block, TotalAmount: t, Price: p}, nil
}

// Remaining is the balance per chunk the batch has left: exact, in PLUR.
func (s ChainState) Remaining(b Batch) *big.Int {
	r := new(big.Int).Sub(b.Value, s.TotalAmount)
	if r.Sign() < 0 {
		r.SetInt64(0)
	}
	return r
}

// TimeLeft is how long the batch lasts if the price stays as it is now: an
// estimate, which moves with the price.
func (s ChainState) TimeLeft(b Batch) time.Duration {
	if s.Price.Sign() <= 0 {
		return time.Duration(b.TTL) * time.Second
	}
	blocks := new(big.Int).Quo(s.Remaining(b), s.Price)
	if !blocks.IsInt64() || blocks.Int64() > int64(1<<62)/int64(BlockTime) {
		return time.Duration(1<<62 - 1)
	}
	return time.Duration(blocks.Int64()) * BlockTime
}

// Retrievable asks the node, with GET /stewardship/{ref}, whether every
// chunk of the tree under ref can be retrieved from the network.
func (c *Client) Retrievable(ctx context.Context, ref string) (bool, error) {
	var r struct {
		IsRetrievable bool `json:"isRetrievable"`
	}
	if err := c.getJSON(ctx, "/stewardship/"+ref, &r); err != nil {
		return false, err
	}
	return r.IsRetrievable, nil
}

// Reupload re-stamps every chunk of the tree under ref onto batch and pushes
// them, with PUT /stewardship/{ref}.
func (c *Client) Reupload(ctx context.Context, ref, batch string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.URL+"/stewardship/"+ref, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Swarm-Postage-Batch-Id", batch)
	resp, err := c.do(req, http.StatusOK)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// Chunk fetches the chunk at addr with GET /chunks/{address} and checks that
// it is a content-addressed or a single-owner chunk with that address.
func (c *Client) Chunk(ctx context.Context, addr swarm.Address) (swarm.Chunk, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL+"/chunks/"+addr.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.do(req, http.StatusOK)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	// One byte more than a chunk may hold, so that a longer body fails the check.
	data, err := io.ReadAll(io.LimitReader(resp.Body, swarm.SocMaxChunkSize+1))
	if err != nil {
		return nil, fmt.Errorf("GET /chunks/%s: %w", addr, err)
	}
	ch := swarm.NewChunk(addr, data)
	if !cac.Valid(ch) && !soc.Valid(ch) {
		return nil, fmt.Errorf("GET /chunks/%s: %w", addr, ErrInvalidChunk)
	}
	return ch, nil
}

// PutChunk uploads one chunk, content-addressed or single-owner, with POST
// /chunks, stamped from batch. A single-owner chunk carries its owner's
// signature, so no key is needed to stamp it again.
func (c *Client) PutChunk(ctx context.Context, batch string, ch swarm.Chunk) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL+"/chunks", bytes.NewReader(ch.Data()))
	if err != nil {
		return err
	}
	req.Header.Set("Swarm-Postage-Batch-Id", batch)
	req.Header.Set("Swarm-Deferred-Upload", "false")
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := c.do(req, http.StatusCreated)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var r struct{ Reference swarm.Address }
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return fmt.Errorf("POST /chunks: %w", err)
	}
	if !r.Reference.Equal(ch.Address()) {
		return fmt.Errorf("POST /chunks: the node stored %s, want %s", r.Reference, ch.Address())
	}
	return nil
}

// FeedIndex returns the index of the latest update of the sequence feed
// (owner, topic) that the node finds with GET /feeds/{owner}/{topic}, or an
// error that Is ErrNotFound when it finds none. The node's lookup can miss
// the newest updates, so callers probe on from it.
func (c *Client) FeedIndex(ctx context.Context, owner, topic []byte) (uint64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/feeds/%x/%x", c.URL, owner, topic), nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Swarm-Only-Root-Chunk", "true") // the index is all we want
	resp, err := c.do(req, http.StatusOK)
	if err != nil {
		return 0, err
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, swarm.SocMaxChunkSize))
	resp.Body.Close()
	h := resp.Header.Get("Swarm-Feed-Index")
	idx, err := hex.DecodeString(h)
	if err != nil || len(idx) != 8 {
		return 0, fmt.Errorf("GET /feeds: Swarm-Feed-Index %q, want 16 hex digits", h)
	}
	return binary.BigEndian.Uint64(idx), nil
}
