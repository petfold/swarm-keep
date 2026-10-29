# Asks to the nodes: which of my batches keep a file alive

Drafts for an issue on Bee (ethersphere/bee) and one on Ant
(solardev-xyz/ant). Decided on 2026-10-29: ask only the uploader's own node
about its own batches, for the privacy reasons below, and file both after
swarm-keep's first real rotation, with the tool to show.

---

## API: which of this node's batches stamp the chunks under a reference

### Problem

Someone who keeps content alive has to know which of their batches stamp
it, and, after re-uploading it onto a new batch with `PUT /stewardship`,
whether all of it moved. Today the node says only whether content is
retrievable (`GET /stewardship`), not which batch keeps it. So tools keep
their own records of what went where (swarm-keep's keep-lists, Freedom's
publish history) and have no way to check those records against the node.

### The node already knows

A node's stamp issuer records every chunk it stamps, per batch:
`postage.StampItem` (batch ID, chunk address, index, timestamp) in
`pkg/postage/stampissuer.go`. It is the record that lets a chunk stamped
again on the same batch keep its slot.

### Ask

1. **One chunk.** `GET /stamps/{batch_id}/chunks/{address}`: whether this
   node stamped the chunk with its batch, with the index and timestamp; 404
   when it did not.
2. **A whole reference.** `GET /stewardship/{reference}/stamps`: walk the
   tree as stewardship does and report, for each of the node's own batches,
   how many of the tree's chunks it stamps, and how many chunks none of them
   stamp.

Both answer from the node's own records. Nothing is asked of other nodes,
and the protocol does not change.

The answers name batches and nothing about time. How long a batch lasts the
caller works out from what the node already serves: the batch's `value`
(`GET /batches/{id}`) and the chain's `totalAmount` and `currentPrice`
(`GET /chainstate`). That is an estimate, since the price moves, and it
stays with the caller.

### Privacy

- A stamp links a chunk to a batch, through the batch to its owner's address
  on chain, and to every other chunk stamped by the same batch, encrypted
  ones included.
- Today a chunk's stamp reaches the nodes that store it and their neighbours,
  and a few stamps appear on chain in storage-reward claims. Linking content
  to its uploader takes a node in the right neighbourhood.
- These endpoints answer only the batch's owner, about chunks it stamped
  itself, through its own API, like `/stamps` and `/wallet`. They tell no one
  anything they did not already know. A gateway that stamps uploads for
  others should keep them closed, as it keeps `/stamps` closed.
- Deliberately not asked:
  - **Stamps in retrieval.** The `Delivery.Stamp` field of the retrieval
    protocol has been empty since 1.17.0. Filling it would let anyone who
    downloads content link it to its uploader. As we read #3936, it was
    emptied because retrieved chunks now go to a cache that keeps no stamps,
    not for privacy; but the privacy cost of filling it again is the reason
    not to.
  - **Storers reporting a chunk's batch or expiry to anyone.** Even an
    expiry date without the batch nearly identifies the batch, and a storer
    could answer falsely.

Content someone else uploaded needs neither: whoever wants it kept stamps it
onto their own batch.

---

## Ant

The same endpoints, for parity with Bee, so tools work against both nodes.
Ant issues stamps itself, so it can keep the same record. Freedom could
then show, for each of the user's own publications, which batch keeps it
and until about when.
