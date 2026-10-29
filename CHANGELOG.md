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
