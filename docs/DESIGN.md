# swarm-keep: design

## The problem

Data on Swarm lives exactly as long as the postage batch that stamps it. A
project keeps data on several batches, uploaded by several tools, and each
tool knows only what it uploaded itself. So nobody can say, for a given
file, which batch keeps it alive or for how long; nothing moves content onto
a new batch when the old one runs out; and every old version stays as long
as its batch does, whether anyone needs it or not.

## What swarm-keep is

- **A keep-list format** ([FORMAT.md](FORMAT.md)): a JSON file saying what
  must stay on Swarm. Any program, in any language, writes one.
- **One program**, `swarm-keep`, that reads the lists and talks to a node's
  HTTP API (Bee, or Ant, which serves the same API):
  - `check`: for everything kept, which batch, about how long it has left,
    whether it is retrievable; one report, one notification;
  - `where <ref>`: which list keeps a reference, on which batch, for how long;
  - `rotate`: re-stamp the lists onto a new batch, verify, record.
- **No keys.** Re-stamping copies chunks that are already signed, so
  swarm-keep never holds a feed owner's key.

## Decisions

Peter, 2026-10-29:

- **Language-neutral.** A file format and a command, not a library: the
  projects that need it are written in Go, Rust, TypeScript and Python.
  Each project supplies only an exporter for structures that only it
  understands.
- **Go, one program,** using Bee's own packages for chunk and feed addressing
  rather than a copy that can drift. It installs as one file; code can move
  into Bee later. Repository `petfold/swarm-keep`, public.
- **Versions:** keep the latest and one previous version by default, per
  list or per item.
- **Notifications:** an exit code and JSON (for cron) and a desktop
  notification first; e-mail and webhooks later, since they need a sending
  service.
- **Outreach** to Bee, Ant, Freedom and Solar Punk's File Manager team after
  the first real rotation, with the tool to show.
- **Privacy: ask only the owner's node about its own batches.** A stamp
  links content to its uploader, so swarm-keep never asks other nodes which
  batch stamps a chunk, and the ask to Bee and Ant is for the owner's own
  records only (drafts for [Bee](upstream/bee.md) and [Ant](upstream/ant.md)).
- **swarmfs writes keep-list entries** for Python projects, and **holdings
  reads swarm-keep's times** per file. Neither becomes a second rotation
  engine.

## How keeping works

- **Trees** (a file, a site, raw bytes): `PUT /stewardship/{ref}` with a
  `swarm-postage-batch-id` header re-stamps every chunk of the tree onto that
  batch, with its erasure-coding replicas (Bee's `pkg/steward`; Ant's gateway
  does the same).
- **Single chunks**: fetched and posted again with the new batch. A
  content-addressed chunk goes to `POST /chunks`; a single-owner chunk (a
  feed update) to `POST /soc/{owner}/{id}?sig=…`, with the owner, id and
  signature it carries, so no key is needed. Never a single-owner chunk to
  `POST /chunks`: Bee reads any body that fits in a chunk as a
  content-addressed chunk (`pkg/api/chunk.go`) and would store it under
  another address.
- **Checking** goes through `GET /stewardship/{ref}` for every target: the
  node retrieves every chunk from the network, not from its own store, and
  treats a single-owner chunk as a tree of one chunk (`pkg/traversal`).
- **Structures only their owner understands** (recordstore stores, swarmblk
  disks): the owner's exporter lists their references (`walk`, `cmd`).
- **Through the batch's own node.** A batch's stamp issuer lives in the node
  that bought it, so re-stamping goes through that node.
- **Immutable batches** for kept data: a full mutable batch overwrites its
  oldest chunks.

## Rotation

`swarm-keep rotate -to <batch>`:

1. **Refuses** a batch that is not the node's own (only the node that bought
   a batch can stamp with it), not usable yet, or mutable.
2. **Lists** every item of every list before stamping anything; an item
   that cannot be listed refuses the whole rotation.
3. **Re-stamps** every target, several at a time. A full bucket (`402`)
   stops everything.
4. **Verifies** all, a sample (the default) or none of what moved.
5. **Records**: a list whose targets all moved and verified names the new
   batch (FORMAT.md, "Rotation rewrites the list"); one with a failure stays
   as it was. Each rotation is appended to
   `$XDG_STATE_HOME/swarm-keep/rotations.jsonl`, with the batch's fill
   before and after.

Running it again after a failure is safe: Bee's stamp issuer keeps a
chunk's slot when the chunk is stamped again on the same batch (seen live:
rotating the market page onto its own batch left the fullest bucket at 24
of 64). Buying the new batch is not part of it: spending is the owner's
decision.

## Which batch, and how long

Which batch, from these sources:

1. the keep-list (`batch`), which `rotate` rewrites;
2. later, other tools' records: Freedom's publish history (a reference and a
   batch per publication), swarmfs's journal (each root's batch and time at
   confirmation), the File Manager's drives (one batch each);
3. the node that owns the batch, once it can say: it records every chunk it
   stamps, per batch, but no API returns that record yet
   (drafts for [Bee](upstream/bee.md) and [Ant](upstream/ant.md)). With
   it, `rotate` could also prove that every chunk landed on the new batch;
   today it shows that the content is retrievable.

Only the owner's node is asked. Which batch stamps someone else's content
is theirs to know: a stamp leads to the uploader's address. To keep such
content, stamp it onto your own batch.

How long: `GET /batches/{id}` gives any batch's `value` (not only the node's
own batches), and `/chainstate` the chain's `totalAmount` and
`currentPrice`. The balance left per chunk, `value − totalAmount`, is exact.
The time, balance ÷ price × 5 s (Gnosis's block time), is an estimate at
today's price, so it is always reported as "about", with the balance beside
it. A file lives as long as its worst-covered chunk.

## Versions

- Every update chunk of a feed is kept: one chunk each, and a reader looks
  for the earlier updates while it finds the latest.
- The content of the latest N versions is re-stamped at rotation.
  Re-stamping a version re-stamps the chunks it shares with older ones, so
  keeping one previous version costs only the difference.
- Older versions stay on the old batch and expire with it. An immutable
  batch frees nothing early, so an unwanted version lingers at most one
  batch lifetime.
- Later: reusing slots of a mutable batch for data uploaded through
  swarm-keep (etherchunk's idea).

## Notifications

One report for everything kept, in terms of content ("the market page has
about 9 days left"), not batch IDs alone. Outputs: an exit code and JSON; a
desktop notification (Linux `notify-send`, macOS `osascript`); later e-mail,
webhooks, and a status file that other programs read: Freedom's planned
place for warnings (its issues #589 and #759), Swarm Desktop, holdings.

## The ecosystem, as checked on 2026-10-29

- **Bee 2.8.3:** stewardship re-uploads onto a named batch;
  `/batches/{id}` for any batch; retrieval stopped carrying a chunk's stamp
  in 1.17.0, when retrieved chunks moved to a cache that keeps no stamps.
  The stamp issuer records every chunk it stamps (`postage.StampItem`).
- **Freedom and Ant:** Freedom's node is Ant (Rust), with Bee's API,
  stewardship included. Freedom's publish history records each
  publication's reference and batch; its publish card warns under 7 days;
  stable addresses are feeds whose old versions stay "while their postage
  lasts". Freedom bundles native programs, and plans a shared Rust core
  (#644).
- **Swarm Desktop and the File Manager** (`file-manager-lib`): a drive is
  one batch, with a TTL warning, "Extend TTL" and version history in feeds.
  Destroying a drive dilutes its batch so that it expires within two days.
  Nothing moves content from one batch to another.
- **Solar Punk tools:** `stamp-monitor` (TypeScript: batch expiry from the
  chain, reports by e-mail; per batch, not per content) and
  `postage-batcher` (a Gnosis contract that tops up or creates several
  batches in one transaction).
- **Others:** swarm-cli (stamps, re-upload), Beeport and Swarmy (buying
  without a node), etherchunk (client-side stamping with slot reuse; files
  and folders only).
- **petfold:** swarmfs (stamp planning and buying in Python, a journal of
  roots and batches), holdings (a catalogue of which media hold which files;
  Swarm as a leased medium, one lease per medium).

## Roadmap

- **v0.1, before 2026-11-09:** the format, `check`, `where` and `rotate`
  (built), a recordstore exporter, and the first rotation of the loopmarket
  and ontodag data off batch `c931c8a5…`.
- **Then:** importers (Freedom, swarmfs, File Manager); the status file;
  e-mail and webhooks; reading the chain without a node; `plan` (size a
  batch for a set); buying several batches at once through
  `postage-batcher`; slot reuse.
