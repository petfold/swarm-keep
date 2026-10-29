# Draft issue for Ant (freedom-hq/ant)

To file after swarm-keep's first real rotation, and after the Bee issue
([bee.md](bee.md)), whose number goes in below (decided 2026-10-29). In
the style of Ant's own issues: code links at a commit, and what was read
versus run. Before filing: move the links to Ant's current main and recheck
them.

---

**Title:** API: which of this node's batches stamp a chunk, or the chunks under a reference (Bee parity)

Ant keeps, for each batch it issues stamps from, the latest stamp of every
chunk it stamped: the issuer's `seen` map, persisted in the `.stamps`
sidecar so that a chunk stamped again keeps its slot
([`crates/ant-postage/src/lib.rs` L105–L118](https://github.com/freedom-hq/ant/blob/e1c643a42c0e4374a6591fcf463c12768ea226fd/crates/ant-postage/src/lib.rs#L105-L118)).
No endpoint reads it. The gateway serves Bee's `GET` and `PUT
/stewardship/{address}`
([`crates/ant-gateway/src/router.rs` L177–L180](https://github.com/freedom-hq/ant/blob/e1c643a42c0e4374a6591fcf463c12768ea226fd/crates/ant-gateway/src/router.rs#L177-L180)),
so a tool can move content onto a new batch and check it is retrievable.
It cannot check that the content is now on the new batch: while the old
batch lasts, a chunk the move skipped is still retrievable.

**Ask:** the two read-only endpoints proposed for Bee in ethersphere/bee#NNNN,
answered from the issuer's own records:

1. `GET /stamps/{batch_id}/chunks/{address}`: whether this node stamped the
   chunk with this batch, with the stamp's index and timestamp; `404` when
   it did not. One lookup in that batch's `seen` map.
2. `GET /stewardship/{reference}/stamps`: walk the tree as
   `GET /stewardship/{reference}` does, and report for each of the node's
   own batches how many of the tree's chunks it stamps, and how many none
   of them stamp.

They answer only the batch's owner, about its own chunks, like `/stamps`
and `/wallet`. They name batches and nothing about time: how long a batch
lasts, the caller works out from `GET /batches/{id}` and `GET /chainstate`,
which Ant already serves. Stamps in retrieval, and storers reporting a
chunk's batch, are deliberately not asked: either would let others link
content to its uploader.

**For Freedom:** its publish history records each publication's
reference and batch. With (2) it could check that record against the node,
and show for each of the user's publications which batch keeps it and
until about when.

**For tools:** [swarm-keep](https://github.com/petfold/swarm-keep) moves kept
content onto a new batch before the old one runs out, through Bee's API.
With these endpoints it works the same against Ant.

Read from the source on main (e1c643a, `ant-gateway` 0.5.77). Not run
against a node.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
