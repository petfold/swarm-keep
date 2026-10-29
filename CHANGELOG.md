# Changelog

## Unreleased

- The keep-list format, version 1 (`docs/FORMAT.md`).
- `swarm-keep check`: for every item of every keep-list, the batch, the
  time left (an estimate at today's price, with the exact balance per
  chunk), and whether the network has it (a sample, or everything with
  `-deep`); text or `-json`, exit status 0/1/2, `-notify` for a desktop
  notification.
- `swarm-keep where <ref>`: which keep-list item keeps a reference, on which
  batch, for how long, and whether it is retrievable.
- Feeds are kept whole (every update chunk) with the content of their latest
  versions (two by default).
- `swarm-keep rotate -to <batch>`: re-stamp every list onto a batch the node
  owns (trees through stewardship, content-addressed chunks through
  `/chunks`, single-owner chunks through `/soc`), verify, rewrite each list
  that fully moved, and log the rotation; `-dry-run`, `-verify
  all|sample|none`. It refuses a batch that is not the node's, not usable or
  mutable, and stops at a full bucket.
- `note` on the list as well as on items: JSON has no comments, so a list
  explains itself in notes, which `rotate` writes back.
- `redundancy` on an item: the erasure-coding level its content was
  uploaded with, passed to the node when checking and re-stamping.
- `check` asks the network for single chunks through stewardship too, so a
  copy in the node's own store no longer counts as retrievable.
