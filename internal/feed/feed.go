// Package feed finds the updates of a sequence feed, and the content each
// update names, with Bee's own feed addressing.
package feed

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethersphere/bee/v2/pkg/feeds"
	"github.com/ethersphere/bee/v2/pkg/soc"
	"github.com/ethersphere/bee/v2/pkg/swarm"

	"github.com/petfold/swarm-keep/internal/bee"
)

// Feed is a sequence feed, Bee's default kind.
type Feed struct {
	Owner common.Address
	Topic []byte
}

// index is a sequence feed index, which Bee's pkg/feeds/sequence encodes as
// 8 bytes, big-endian, but does not export.
type index uint64

func (i index) MarshalBinary() ([]byte, error) {
	return binary.BigEndian.AppendUint64(nil, uint64(i)), nil
}

func (i index) Next(int64, uint64) feeds.Index { return i + 1 }

func (i index) String() string { return strconv.FormatUint(uint64(i), 10) }

// Address returns the address of the single-owner chunk that holds update i.
func (f Feed) Address(i uint64) (swarm.Address, error) {
	return feeds.New(f.Topic, f.Owner).Update(index(i)).Address()
}

// Latest returns the number of updates the feed has: the node's lookup,
// checked by probing the updates after it, which the lookup can miss. It
// returns 0 for a feed with no updates.
func Latest(ctx context.Context, c *bee.Client, f Feed) (uint64, error) {
	var n uint64
	i, err := c.FeedIndex(ctx, f.Owner[:], f.Topic)
	switch {
	case errors.Is(err, bee.ErrNotFound):
	case err != nil:
		return 0, err
	default:
		n = i + 1
	}
	for {
		addr, err := f.Address(n)
		if err != nil {
			return 0, err
		}
		_, err = c.Chunk(ctx, addr)
		if errors.Is(err, bee.ErrNotFound) {
			return n, nil
		}
		if err != nil {
			return 0, fmt.Errorf("feed update %d: %w", n, err)
		}
		n++
	}
}

// Content returns what a feed update names. An update in Bee's legacy form,
// a timestamp and a reference (32 bytes, or 64 for encrypted content), names
// its content by that reference, and ref is it. Any other update carries its
// content inside itself; then ref is empty and span is the content's length,
// which is all of it when it fits in the one chunk.
func Content(update swarm.Chunk) (ref []byte, span uint64, err error) {
	s, err := soc.FromChunk(update)
	if err != nil {
		return nil, 0, fmt.Errorf("feed update %s: %w", update.Address(), err)
	}
	data := s.WrappedChunk().Data()
	if len(data) < swarm.SpanSize {
		return nil, 0, fmt.Errorf("feed update %s: %d bytes", update.Address(), len(data))
	}
	span = binary.LittleEndian.Uint64(data[:swarm.SpanSize])
	// The same lengths Bee's GET /feeds treats as legacy: span, timestamp,
	// reference.
	if n := len(data); n == swarm.SpanSize+8+swarm.HashSize || n == swarm.SpanSize+8+2*swarm.HashSize {
		return data[swarm.SpanSize+8:], span, nil
	}
	return nil, span, nil
}
