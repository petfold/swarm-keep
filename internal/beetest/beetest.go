// Package beetest is a fake node for tests: the few endpoints of Bee's API
// that swarm-keep calls, over an in-memory store, with feeds written by Bee's
// own updater.
package beetest

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethersphere/bee/v2/pkg/crypto"
	"github.com/ethersphere/bee/v2/pkg/feeds"
	"github.com/ethersphere/bee/v2/pkg/feeds/sequence"
	"github.com/ethersphere/bee/v2/pkg/storage"
	"github.com/ethersphere/bee/v2/pkg/swarm"
)

// Price is the fake chain's price, in PLUR per chunk per block.
const Price = 100

// TotalAmount is what the fake chain has drawn per chunk so far.
const TotalAmount = 1_000_000

// Node is a fake node.
type Node struct {
	*httptest.Server

	mu       sync.Mutex
	chunks   map[string][]byte // hex address → data
	batches  map[string]*big.Int
	trees    map[string]bool // stewardship answers: hex reference → retrievable
	feedLag  map[string]uint64
	Requests []string // "METHOD path", in order
}

// New starts a fake node, closed when t ends.
func New(t testing.TB) *Node {
	n := &Node{chunks: map[string][]byte{}, batches: map[string]*big.Int{}, trees: map[string]bool{}, feedLag: map[string]uint64{}}
	n.Server = httptest.NewServer(http.HandlerFunc(n.serve))
	t.Cleanup(n.Close)
	return n
}

// Batch adds a batch that lasts d at the fake chain's price.
func (n *Node) Batch(id string, d time.Duration) {
	blocks := int64(d / (5 * time.Second))
	v := big.NewInt(TotalAmount + Price*blocks)
	n.mu.Lock()
	defer n.mu.Unlock()
	n.batches[id] = v
}

// Tree sets what stewardship answers for ref.
func (n *Node) Tree(ref string, retrievable bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.trees[ref] = retrievable
}

// Chunk stores a chunk.
func (n *Node) Chunk(ch swarm.Chunk) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.chunks[ch.Address().String()] = ch.Data()
}

// Feed writes updates with Bee's sequence updater, under a new key, and
// returns the owner and topic. The node's lookup will report lag updates
// fewer than there are, as a real node's lookup can.
func (n *Node) Feed(t testing.TB, topic []byte, lag uint64, payloads ...[]byte) common.Address {
	key, err := crypto.GenerateSecp256k1Key()
	if err != nil {
		t.Fatal(err)
	}
	signer := crypto.NewDefaultSigner(key)
	u, err := sequence.NewUpdater(storage.PutterFunc(func(_ context.Context, ch swarm.Chunk) error {
		n.Chunk(ch)
		return nil
	}), signer, topic)
	if err != nil {
		t.Fatal(err)
	}
	for i, p := range payloads {
		if err := u.Update(context.Background(), int64(i), p); err != nil {
			t.Fatal(err)
		}
	}
	owner, err := signer.EthereumAddress()
	if err != nil {
		t.Fatal(err)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.feedLag[fmt.Sprintf("%x/%x", owner[:], topic)] = lag
	return owner
}

// LegacyPayload is a feed update payload in Bee's legacy form: a timestamp
// and a reference.
func LegacyPayload(ref []byte) []byte {
	return append(binary.BigEndian.AppendUint64(nil, uint64(time.Now().Unix())), ref...)
}

func (n *Node) serve(w http.ResponseWriter, r *http.Request) {
	n.mu.Lock()
	n.Requests = append(n.Requests, r.Method+" "+r.URL.Path)
	n.mu.Unlock()
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/chainstate":
		writeJSON(w, map[string]any{"block": 1234, "chainTip": 1234, "totalAmount": fmt.Sprint(TotalAmount), "currentPrice": fmt.Sprint(Price)})
	case r.Method == http.MethodGet && len(parts) == 2 && parts[0] == "batches":
		n.mu.Lock()
		v, ok := n.batches[parts[1]]
		n.mu.Unlock()
		if !ok {
			notFound(w, "issuer does not exist")
			return
		}
		ttl := new(big.Int).Sub(v, big.NewInt(TotalAmount))
		ttl.Quo(ttl, big.NewInt(Price)).Mul(ttl, big.NewInt(5))
		writeJSON(w, map[string]any{"batchID": parts[1], "value": v.String(), "owner": "bd93168814b2786bcaf8783d3ca2348bf193ccfd", "depth": 20, "bucketDepth": 16, "immutable": true, "batchTTL": ttl.Int64()})
	case r.Method == http.MethodGet && len(parts) == 2 && parts[0] == "stewardship":
		n.mu.Lock()
		ok, known := n.trees[parts[1]]
		n.mu.Unlock()
		if !known {
			notFound(w, "Not Found")
			return
		}
		writeJSON(w, map[string]any{"isRetrievable": ok})
	case r.Method == http.MethodGet && len(parts) == 2 && parts[0] == "chunks":
		n.mu.Lock()
		data, ok := n.chunks[parts[1]]
		n.mu.Unlock()
		if !ok {
			notFound(w, "Not Found")
			return
		}
		w.Header().Set("Content-Type", "binary/octet-stream")
		w.Write(data)
	case r.Method == http.MethodGet && len(parts) == 3 && parts[0] == "feeds":
		n.feed(w, parts[1], parts[2])
	default:
		http.Error(w, `{"message":"not in the fake node","code":501}`, http.StatusNotImplemented)
	}
}

// feed answers a lookup with the index of the latest update it finds, lag
// updates short of the last.
func (n *Node) feed(w http.ResponseWriter, ownerHex, topicHex string) {
	n.mu.Lock()
	lag := n.feedLag[ownerHex+"/"+topicHex]
	n.mu.Unlock()
	owner, _ := hex.DecodeString(ownerHex)
	topic, _ := hex.DecodeString(topicHex)
	f := feeds.New(topic, common.BytesToAddress(owner))
	var count uint64
	for {
		a, _ := f.Update(index(count)).Address()
		n.mu.Lock()
		_, ok := n.chunks[a.String()]
		n.mu.Unlock()
		if !ok {
			break
		}
		count++
	}
	if count <= lag {
		notFound(w, "no update found")
		return
	}
	w.Header().Set("Swarm-Feed-Index", hex.EncodeToString(binary.BigEndian.AppendUint64(nil, count-1-lag)))
	w.Write([]byte("x"))
}

// index is a sequence feed index as Bee's pkg/feeds/sequence encodes it.
type index uint64

func (i index) MarshalBinary() ([]byte, error) {
	return binary.BigEndian.AppendUint64(nil, uint64(i)), nil
}

func (i index) Next(int64, uint64) feeds.Index { return i + 1 }

func (i index) String() string { return fmt.Sprint(uint64(i)) }

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func notFound(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	json.NewEncoder(w).Encode(map[string]any{"message": msg, "code": 404})
}
