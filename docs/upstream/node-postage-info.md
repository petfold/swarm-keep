# Asks to the nodes: which batches keep a file alive

Drafts for an issue on Bee (ethersphere/bee) and one on Ant
(solardev-xyz/ant). Decided 2026-10-29: file them after swarm-keep's first
real rotation, with the tool to show.

---

## API: which postage batches cover a chunk or a file, and how long they last

### Problem

Someone who wants to keep content alive has to know which batches stamp it.
Today only the uploader's own records can say, and only for what they uploaded
themselves. After a re-upload onto a new batch (`PUT /stewardship`), or for
content someone else published (a site behind an ENS name, a dataset), nothing
tells which batch keeps it alive, or when the network may drop it.

### What nodes already hold

- A storer keeps each chunk's stamp per batch in its reserve
  (`pkg/storer`, `chunkstamp.LoadWithBatchID`).
- The retrieval protocol's `Delivery` message has a `Stamp` field
  (`pkg/retrieval/pb/retrieval.proto`). Bee filled it until 1.17.0 and has
  not since the new storage layer (#3936, June 2023).
- `GET /batches/{batch_id}` already gives any batch's `value` and `batchTTL`,
  and `/chainstate` the current price and `totalAmount`.

### Ask

1. **A chunk's stamps.** `GET /chunks/{address}/stamps` (or a response
   header on `GET /chunks`): the stamps the node knows for the chunk (batch
   ID, index, timestamp, signature), from its own store and, when it
   retrieves the chunk, from the storer.
2. **A file's coverage.** `GET /stewardship/{reference}/stamps`: walk the
   tree as `isRetrievable` does and report, for each batch, how many of the
   tree's chunks it stamps, with the batch's remaining balance per chunk and
   `batchTTL`; and how many chunks came back with no stamp.
3. **Stamps in retrieval.** For 1 and 2 to work for content the node does
   not hold, a delivery carries the storer's stamp (or stamps) for the chunk
   again.

### Time is an estimate

Please report the exact quantities (the batch's remaining balance per chunk,
`value` minus `totalAmount`, and the current price) beside `batchTTL`, and
label the time as an estimate: it moves with the storage price.

### Questions

- Why did the stamp leave the delivery: bandwidth, or privacy? A stamp links
  a chunk to a batch, and so to every other chunk of the same batch; batch
  owners are public on chain. If that link is the concern, an opt-in, or
  answers limited to the node's own batches, would still cover 1 for
  uploaders.
- With several stamps for one chunk (a re-upload onto a new batch keeps the
  old stamp until its batch expires), should the storer return all of them,
  or the one that lasts longest?

---

## Ant

The same endpoints, for parity with Bee, so tools work against both nodes.
Freedom already shows a batch's time on the publish card and the storage
screen; with these, it could also say, for any page, "kept by batch X until
about Y".
