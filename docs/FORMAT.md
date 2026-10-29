# The keep-list format, version 1

A keep-list is a JSON file that says what must stay on Swarm. Any program, in
any language, can write one. swarm-keep reads them and checks, warns and
re-stamps. The file is the contract between swarm-keep and the projects whose
data it keeps, so this page is normative: swarm-keep refuses a list that does
not follow it, and names the field.

## Example

```json
{
  "keep": 1,
  "name": "loopmarket",
  "batch": "c931c8a5ee8def225abf86a934bbf38ab191f10456f1a684c9cb44f534834359",
  "versions": 2,
  "warn": "14d",
  "items": [
    {"name": "market page", "ref": "2abb4979d03acd202fe547cdb1902b86b06bd9ce16c341cd9cfe4d9a2e6b5442"},
    {"name": "market page feed",
     "feed": {"owner": "0x1f2e…", "topic": "5a1c…", "manifest": "a0e5384a…"}},
    {"name": "page maker's book",
     "feed": {"owner": "0xe2bfb757…", "topic_string": "loop-e2bfb757"},
     "walk": ["recordstore", "refs", "{ref}"]},
    {"name": "test data", "list": "test-refs.txt"},
    {"name": "core pack v14", "ref": "9f3b…", "walk": ["recordstore", "refs", "{ref}"]}
  ]
}
```

## The list

| field | | meaning |
|---|---|---|
| `keep` | required | the format version: `1` |
| `name` | required | shown in reports and notifications |
| `batch` | optional | the postage batch (64 hex) that stamps everything in the list now; an item may name its own. `swarm-keep rotate` rewrites it (below) |
| `versions` | optional | for feeds: how many of the latest versions' content to keep. A number from 1, or `"all"`. Default `2`: the latest and one before it |
| `warn` | optional | warn when anything is estimated to have less than this left. A number with `h`, `d` or `wk`, such as `36h`, `14d`, `2wk`. Default `14d` |
| `items` | required | what to keep, below |

Unknown fields are refused, so a typo never passes silently. A newer format
gets a new `keep` number.

## Items

Every item has a `name` and exactly one of `ref`, `chunk`, `feed`, `list` or
`cmd`.

| field | meaning |
|---|---|
| `name` | shown in reports |
| `ref` | a Swarm reference: 64 hex, or 128 for encrypted content. The node walks everything under it: a file, a folder or site, raw bytes |
| `chunk` | one chunk address (64 hex), content-addressed or single-owner, kept as it is |
| `feed` | a sequence feed: `{"owner": …, "topic": …}`, below |
| `list` | a text file of entries, one per line, below. A relative path is read from the keep-list's own directory |
| `cmd` | a command, as an array of arguments, whose output is entries, one per line |
| `walk` | with `ref` or `feed`: a command, as an array of arguments, one of which contains `{ref}`. For content the node cannot walk, it prints the entries under that reference. It runs for the `ref`, or for each kept version of the feed |
| `redundancy` | the erasure-coding level the item's content was uploaded with: `0` none (the default), `1` medium, `2` strong, `3` insane, `4` paranoid. The node needs it to check the content and to re-stamp the root's dispersed replicas. Not with `chunk` |
| `batch` | the batch that stamps this item, if not the list's |
| `versions` | with `feed`: overrides the list's `versions` |
| `note` | free text, ignored |

### Feeds

| field | meaning |
|---|---|
| `owner` | the feed owner's Ethereum address: 40 hex, with or without `0x` |
| `topic` | the topic: 64 hex |
| `topic_string` | instead of `topic`: a string, hashed with Keccak-256 as bee-js does |
| `manifest` | optional: the feed manifest reference that names (ENS) and links point to; kept like a `ref` |

A feed is kept whole: every update chunk, from the first to the latest, stays
stamped, because a reader finding the latest update looks for the earlier
ones. The content is kept only for the latest `versions` updates. An update
written in Bee's legacy form (a timestamp and a reference) names its content
by that reference; any other update carries its content inside itself.

Older versions' content is not re-stamped when a batch rotates, so it stays on
the old batch and lasts as long as that batch does.

### Entries

`list`, `cmd` and `walk` produce entries, one per line:

| line | meaning |
|---|---|
| `<hex>` or `ref <hex>` | a reference, kept like a `ref` item |
| `chunk <hex>` | one chunk, kept like a `chunk` item |
| empty, or starting with `#` | ignored |

A command that exits with an error fails the item; swarm-keep never keeps
half a list without saying so.

## Rotation rewrites the list

`swarm-keep rotate` moves a whole list onto one batch. Once every tree and
chunk of the list has moved and the ones it checked are retrievable, it
writes the list back with `batch` set to the new batch and every item's own
`batch` removed. It writes the file in its own layout (two-space indents,
fields in the order of this page), through a new file renamed over the old
one. A program that generates a keep-list should carry `batch` over when it
writes the list again.

## Time left is an estimate

A batch holds a balance per chunk, exact on chain, and the network draws on
it at the storage price, which changes. So the time a batch has left is an
estimate at today's price: sooner if the price rises, later if it falls.
swarm-keep reports the time as "about", shows the exact balance beside it,
and warns early enough (`warn`) to leave room for a rise.
