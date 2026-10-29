# CLAUDE.md — swarm-keep

swarm-keep keeps data alive on Ethereum Swarm. A keep-list (a JSON file any
program writes) says what must stay; `swarm-keep` checks how long each item
has left, warns, and re-stamps the lists onto a new batch before the old one
runs out. This file holds what is true now and the rules; the reasons are in
`docs/DESIGN.md`, and what changed in `CHANGELOG.md`.

## Read first

1. [docs/DESIGN.md](docs/DESIGN.md): what it is, the decisions, the ecosystem
2. [docs/FORMAT.md](docs/FORMAT.md): the keep-list format, normative
3. [docs/upstream/node-postage-info.md](docs/upstream/node-postage-info.md): what we ask of Bee and Ant

## Where we are

v0.1 in progress. Built: the format (`internal/keeplist`), the node client
(`internal/bee`), feeds (`internal/feed`), `check`, `where` and `rotate`
(`internal/keep`, `cmd/swarm-keep`), desktop notifications
(`internal/notify`). `rotate` ran live on 2026-10-29: the loopmarket page's
list onto its own batch, 9 targets, all verified, no new slots. Next:
recordstore's exporter, our keep-lists, then the first rotation of the
loopmarket and ontodag data off batch `c931c8a5…` (expires about
2026-11-09) onto a batch Peter buys.

## Rules

- **Ask before spending.** Buying, topping up or diluting a batch spends
  xBZZ: only on Peter's word. swarm-keep never buys on its own.
- **No keys.** swarm-keep re-stamps chunks that are already signed and never
  needs a private key. Never print key material, and never print an RPC URL
  that can carry one.
- **Public repository.** No secrets, and no real keep-lists: ours live in
  `~/.config/swarm-keep/`.
- **Bee's code for Bee's formats.** Chunk, single-owner chunk and feed
  addressing come from `github.com/ethersphere/bee/v2`; never reimplement
  BMT or feed addressing.
- **Time left is an estimate.** Say "about", and show the exact balance per
  chunk beside it.
- **Re-stamp through the batch's own node**, onto immutable batches only.
- **Ask only the owner's node about its own batches.** A stamp links content
  to its uploader's address, so never ask other nodes, or build anything
  that asks them, which batch stamps a chunk (Peter, 2026-10-29).
- **Single-owner chunks go to `POST /soc`**, never `POST /chunks`, which
  stores a small one as a content-addressed chunk under another address.
- **The format is a contract.** A change to `docs/FORMAT.md` that a reader
  of version 1 would misread needs a new `keep` number.

## Stack and environment

- Go, at the version in `go.mod`; `github.com/ethersphere/bee/v2` v2.8.3 as a
  library, as swarmblk and s3warm use it.

| Variable | Meaning |
|---|---|
| `SWARM_KEEP_BEE_URL` | the node's API, default `http://localhost:1633` |
| `XDG_CONFIG_HOME` | keep-lists are read from `$XDG_CONFIG_HOME/swarm-keep/*.json` (default `~/.config/swarm-keep`) |

Unit tests need no network: the node is an `httptest` server.

## Commands

```bash
go test ./...
go vet ./...
gofmt -l .            # must print nothing
go run ./cmd/swarm-keep check
```

## Layout

```
cmd/swarm-keep/     the command: check, where, rotate
internal/keeplist/  the format: reading, checking, entries
internal/bee/       the node's HTTP API
internal/feed/      sequence feeds: update addresses, the content an update names
internal/keep/      expanding items into chunks and trees, checking and rotating them
internal/beetest/   a fake node for tests
internal/notify/    desktop notifications
docs/               design, format, upstream asks
```
