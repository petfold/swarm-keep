package feed_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/ethersphere/bee/v2/pkg/crypto"
	"github.com/ethersphere/bee/v2/pkg/feeds/sequence"
	"github.com/ethersphere/bee/v2/pkg/storage"
	"github.com/ethersphere/bee/v2/pkg/swarm"

	"github.com/petfold/swarm-keep/internal/bee"
	"github.com/petfold/swarm-keep/internal/beetest"
	"github.com/petfold/swarm-keep/internal/feed"
)

// Update addresses are the ones Bee's own sequence updater writes.
func TestAddressMatchesBee(t *testing.T) {
	key, err := crypto.GenerateSecp256k1Key()
	if err != nil {
		t.Fatal(err)
	}
	signer := crypto.NewDefaultSigner(key)
	owner, _ := signer.EthereumAddress()
	topic := bytes.Repeat([]byte{7}, 32)
	var written []swarm.Chunk
	u, err := sequence.NewUpdater(storage.PutterFunc(func(_ context.Context, ch swarm.Chunk) error {
		written = append(written, ch)
		return nil
	}), signer, topic)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		if err := u.Update(context.Background(), int64(i), []byte{byte(i)}); err != nil {
			t.Fatal(err)
		}
	}
	f := feed.Feed{Owner: owner, Topic: topic}
	for i, ch := range written {
		got, err := f.Address(uint64(i))
		if err != nil {
			t.Fatal(err)
		}
		if !got.Equal(ch.Address()) {
			t.Errorf("update %d: %s, Bee wrote %s", i, got, ch.Address())
		}
	}
}

func TestLatestProbesPastTheLookup(t *testing.T) {
	n := beetest.New(t)
	c := bee.New(n.URL, 4)
	topic := bytes.Repeat([]byte{1}, 32)
	for _, lag := range []uint64{0, 2, 5} {
		owner := n.Feed(t, topic, lag, []byte("a"), []byte("b"), []byte("c"), []byte("d"), []byte("e"))
		got, err := feed.Latest(context.Background(), c, feed.Feed{Owner: owner, Topic: topic})
		if err != nil || got != 5 {
			t.Errorf("lookup %d short: %d updates, %v; want 5", lag, got, err)
		}
	}
	got, err := feed.Latest(context.Background(), c, feed.Feed{Topic: topic})
	if err != nil || got != 0 {
		t.Errorf("a feed with no updates: %d, %v", got, err)
	}
}

func TestContent(t *testing.T) {
	n := beetest.New(t)
	c := bee.New(n.URL, 4)
	topic := bytes.Repeat([]byte{2}, 32)
	ref := bytes.Repeat([]byte{0xab}, 32)
	encrypted := bytes.Repeat([]byte{0xcd}, 64)
	owner := n.Feed(t, topic, 0, beetest.LegacyPayload(ref), beetest.LegacyPayload(encrypted), []byte("carried inside"))
	f := feed.Feed{Owner: owner, Topic: topic}
	for i, want := range [][]byte{ref, encrypted, nil} {
		addr, _ := f.Address(uint64(i))
		ch, err := c.Chunk(context.Background(), addr)
		if err != nil {
			t.Fatal(err)
		}
		got, span, err := feed.Content(ch)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("update %d names %x, want %x", i, got, want)
		}
		if want == nil && span != uint64(len("carried inside")) {
			t.Errorf("update %d: span %d", i, span)
		}
	}
}
