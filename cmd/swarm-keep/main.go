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
	case "check", "where":
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
	timeout := fs.Duration("timeout", 30*time.Minute, "give up after this long")
	var sample *int
	var deep, notifyFlag *bool
	if cmd == "check" {
		sample = fs.Int("sample", 8, "how many of each item's trees and chunks to ask the network for")
		deep = fs.Bool("deep", false, "ask the network for every tree and chunk kept")
		notifyFlag = fs.Bool("notify", false, "show a desktop notification when something needs attention")
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if cmd == "where" && fs.NArg() != 1 {
		fmt.Fprintln(stderr, "swarm-keep where: want one reference")
		return 2
	}
	if cmd == "check" && fs.NArg() != 0 {
		fmt.Fprintf(stderr, "swarm-keep check: unexpected %q\n", fs.Arg(0))
		return 2
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
