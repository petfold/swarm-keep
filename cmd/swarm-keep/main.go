// Command swarm-keep keeps data alive on Swarm: it reads keep-lists
// (docs/FORMAT.md), reports how long everything kept has left and whether the
// network has it, and warns.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/petfold/swarm-keep/internal/bee"
	"github.com/petfold/swarm-keep/internal/keep"
	"github.com/petfold/swarm-keep/internal/keeplist"
	"github.com/petfold/swarm-keep/internal/notify"
)

// version is set at release builds with -ldflags "-X main.version=…".
var version = "dev"

const usage = `swarm-keep keeps data alive on Swarm.

Usage:
  swarm-keep check [flags]        how long everything kept has left, and whether the network has it
  swarm-keep where [flags] <ref>  which keep-list keeps a reference, on which batch, for how long
  swarm-keep rotate -to <batch>   re-stamp everything kept onto a new batch, verify it, record it
  swarm-keep version

Keep-lists are read from %s, or from -l.
The node is %s, or -bee.

Exit status: 0 when everything is fine, 1 when something needs attention,
2 when swarm-keep could not do its job.
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

// lists is a repeatable -l flag.
type lists []string

func (l *lists) String() string     { return strings.Join(*l, ",") }
func (l *lists) Set(s string) error { *l = append(*l, s); return nil }

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintf(stderr, usage, configDir(), beeURL())
		return 2
	}
	cmd, args := args[0], args[1:]
	switch cmd {
	case "version", "-version", "--version":
		fmt.Fprintln(stdout, "swarm-keep", version)
		return 0
	case "help", "-h", "-help", "--help":
		fmt.Fprintf(stdout, usage, configDir(), beeURL())
		return 0
	case "check", "where", "rotate":
	default:
		fmt.Fprintf(stderr, "swarm-keep: unknown command %q\n\n", cmd)
		fmt.Fprintf(stderr, usage, configDir(), beeURL())
		return 2
	}

	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	var paths lists
	fs.Var(&paths, "l", "a keep-list file or directory (repeatable)")
	url := fs.String("bee", beeURL(), "the node's API")
	asJSON := fs.Bool("json", false, "write the report as JSON")
	workers := fs.Int("workers", 8, "requests at once")
	limit := 30 * time.Minute
	if cmd == "rotate" {
		limit = 24 * time.Hour
	}
	timeout := fs.Duration("timeout", limit, "give up after this long")
	var sample *int
	var deep, notifyFlag, dryRun *bool
	var to, verify, logPath *string
	switch cmd {
	case "check":
		sample = fs.Int("sample", 8, "how many of each item's trees and chunks to ask the network for")
		deep = fs.Bool("deep", false, "ask the network for every tree and chunk kept")
		notifyFlag = fs.Bool("notify", false, "show a desktop notification when something needs attention")
	case "rotate":
		to = fs.String("to", "", "the batch to move onto: this node's own, usable and immutable")
		dryRun = fs.Bool("dry-run", false, "list what would move, and stop")
		verify = fs.String("verify", "sample", "afterwards, ask the network for all, a sample or none of what moved")
		sample = fs.Int("sample", 8, "with -verify sample, how many of each list's trees and chunks")
		logPath = fs.String("log", logFile(), "append the rotation here as a JSON line; empty for none")
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if cmd == "where" && fs.NArg() != 1 {
		fmt.Fprintln(stderr, "swarm-keep where: want one reference")
		return 2
	}
	if cmd != "where" && fs.NArg() != 0 {
		fmt.Fprintf(stderr, "swarm-keep %s: unexpected %q\n", cmd, fs.Arg(0))
		return 2
	}
	if cmd == "rotate" {
		if len(*to) != 64 {
			fmt.Fprintln(stderr, "swarm-keep rotate: -to wants a batch ID, 64 hex digits")
			return 2
		}
		switch keep.Verify(*verify) {
		case keep.VerifyAll, keep.VerifySample, keep.VerifyNone:
		default:
			fmt.Fprintf(stderr, "swarm-keep rotate: -verify %q: want all, sample or none\n", *verify)
			return 2
		}
	}

	ls, err := load(paths)
	if err != nil {
		fmt.Fprintln(stderr, "swarm-keep:", err)
		return 2
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	b := bee.New(*url, *workers)
	c := &keep.Checker{Bee: b, Expand: &keep.Expander{Bee: b}}

	if cmd == "where" {
		w, err := c.Where(ctx, ls, fs.Arg(0))
		if err != nil {
			fmt.Fprintln(stderr, "swarm-keep:", err)
			return 2
		}
		if *asJSON {
			writeJSON(stdout, w)
		} else {
			w.Text(stdout)
		}
		if len(w.Kept) == 0 || !w.Found {
			return 1
		}
		for _, p := range w.Kept {
			if p.Status != keep.OK {
				return 1
			}
		}
		return 0
	}

	if cmd == "rotate" {
		return rotate(ctx, c, ls, keep.RotateOptions{
			To: strings.ToLower(*to), DryRun: *dryRun, Verify: keep.Verify(*verify), Sample: *sample,
			Workers: *workers, Log: *logPath, Progress: progress(stderr),
		}, *asJSON, stdout, stderr)
	}

	rep, err := c.Check(ctx, ls, keep.Options{Sample: *sample, Deep: *deep, Workers: *workers})
	if err != nil {
		fmt.Fprintln(stderr, "swarm-keep:", err)
		return 2
	}
	if *asJSON {
		writeJSON(stdout, rep)
	} else {
		rep.Text(stdout)
	}
	if rep.Status == keep.OK {
		return 0
	}
	if *notifyFlag {
		if err := notify.Send("swarm-keep", rep.Summary()); err != nil {
			fmt.Fprintln(stderr, "swarm-keep: notification:", err)
		}
	}
	return 1
}

func rotate(ctx context.Context, c *keep.Checker, ls []*keeplist.List, o keep.RotateOptions, asJSON bool, stdout, stderr io.Writer) int {
	rot, err := c.Rotate(ctx, ls, o)
	if rot != nil {
		if asJSON {
			writeJSON(stdout, rot)
		} else {
			rot.Text(stdout)
		}
	}
	switch {
	case err != nil:
		fmt.Fprintln(stderr, "swarm-keep rotate:", err)
		if rot == nil {
			return 2
		}
		return 1
	case rot.DryRun || rot.OK():
		return 0
	}
	return 1
}

// progress reports re-stamping on stderr every few seconds, and at the end.
func progress(w io.Writer) func(done, total, failed int) {
	var mu sync.Mutex
	last := time.Now()
	return func(done, total, failed int) {
		mu.Lock()
		defer mu.Unlock()
		if done < total && time.Since(last) < 5*time.Second {
			return
		}
		last = time.Now()
		fmt.Fprintf(w, "re-stamped %d of %d (%d failed)\n", done, total, failed)
	}
}

// load reads the keep-lists named by -l, files or directories, or else every
// list in the configuration directory.
func load(paths []string) ([]*keeplist.List, error) {
	if len(paths) == 0 {
		dir := configDir()
		ls, err := keeplist.LoadDir(dir)
		if err == nil && len(ls) == 0 {
			err = fmt.Errorf("no keep-lists in %s; name one with -l", dir)
		}
		return ls, err
	}
	var all []*keeplist.List
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if fi.IsDir() {
			ls, err := keeplist.LoadDir(p)
			if err != nil {
				return nil, err
			}
			all = append(all, ls...)
			continue
		}
		l, err := keeplist.Load(p)
		if err != nil {
			return nil, err
		}
		all = append(all, l)
	}
	names := map[string]string{}
	for _, l := range all {
		if prev, ok := names[l.Name]; ok {
			return nil, fmt.Errorf("%s and %s are both named %q", prev, l.Path, l.Name)
		}
		names[l.Name] = l.Path
	}
	return all, nil
}

func configDir() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "swarm-keep")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "swarm-keep"
	}
	return filepath.Join(home, ".config", "swarm-keep")
}

func logFile() string {
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, "swarm-keep", "rotations.jsonl")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "state", "swarm-keep", "rotations.jsonl")
}

func beeURL() string {
	if u := os.Getenv("SWARM_KEEP_BEE_URL"); u != "" {
		return u
	}
	return "http://localhost:1633"
}

func writeJSON(w io.Writer, v any) {
	e := json.NewEncoder(w)
	e.SetIndent("", "  ")
	if err := e.Encode(v); err != nil && !errors.Is(err, io.ErrClosedPipe) {
		fmt.Fprintln(os.Stderr, "swarm-keep:", err)
	}
}
