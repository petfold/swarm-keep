# swarm-keep

[![test](https://github.com/petfold/swarm-keep/actions/workflows/test.yml/badge.svg)](https://github.com/petfold/swarm-keep/actions/workflows/test.yml)
[![license](https://img.shields.io/badge/license-BSD--3--Clause-blue)](LICENSE)

Keeps data alive on [Ethereum Swarm](https://www.ethswarm.org/).

Data on Swarm lasts exactly as long as the postage batch that stamps it. A
project's data sits on several batches, put there by several tools, and
each tool knows only what it uploaded itself. swarm-keep gives one answer
for all of it: which batch keeps each thing, about how long it has left,
whether the network still has it. It warns before anything runs out, and
it will move what you keep onto a new batch, leaving old versions you no
longer need to expire.

**Status: early.** `check` and `where` work; `rotate` comes next. See the
[design](docs/DESIGN.md).

## What to keep: a keep-list

A keep-list is a JSON file that any program, in any language, can write
([format](docs/FORMAT.md)):

```json
{
  "keep": 1,
  "name": "my site",
  "batch": "c931c8a5ee8def225abf86a934bbf38ab191f10456f1a684c9cb44f534834359",
  "items": [
    {"name": "site", "feed": {"owner": "b5f18aab…", "topic": "e40706ae…", "manifest": "a0e5384a…"}},
    {"name": "dataset", "ref": "2abb4979…"},
    {"name": "my app's store", "ref": "9f3b…", "walk": ["my-app", "list-refs", "{ref}"]}
  ]
}
```

A feed is kept whole, every update of it, and the content of its latest two
versions; older versions stay on their old batch and expire with it. Data
whose structure only its own program understands is listed by that program
(`walk`, `cmd`), so swarm-keep needs no plug-ins.

## Use

```bash
go install github.com/petfold/swarm-keep/cmd/swarm-keep@latest

swarm-keep check -l my-site.json             # everything kept: batch, time left, retrievable?
swarm-keep check -notify                     # every list in ~/.config/swarm-keep, with a desktop notification
swarm-keep where -l my-site.json 2abb4979…   # which list keeps it, on which batch, for how long
```

```
1 items: 0 ok, 1 warn, 0 fail (block 49002880, price 196668 PLUR per chunk per block)

loopmarket page
  WARN  market page feed              about 10.5 days left    9 of 9 checked retrievable (9 kept)
        batch c931c8a5…: 35829334865 PLUR per chunk left
        about 10.5 days left on batch c931c8a5…, under the list's warning time of 14 days
```

The time left is an estimate at today's storage price: sooner if the price
rises, later if it falls. The balance beside it is exact.

`check` exits 0 when everything is fine, 1 when something needs attention
and 2 when it could not do its job, so it runs from cron as it is; `-json`
writes the report for other programs. It talks to a node's HTTP API: Bee,
or Ant, which serves the same API (`-bee`, or `SWARM_KEEP_BEE_URL`; default
`http://localhost:1633`). It needs no keys.

## Licence

BSD-3-Clause.
