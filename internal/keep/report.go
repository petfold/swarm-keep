package keep

import (
	"fmt"
	"io"
	"strings"
)

// Attention lists the items that are not OK, worst first.
func (rep *Report) Attention() []ItemReport {
	var out []ItemReport
	for _, s := range []Status{Fail, Warn} {
		for _, r := range rep.Items {
			if r.Status == s {
				out = append(out, r)
			}
		}
	}
	return out
}

// Text writes the report for people: one line per item, its problems under
// it.
func (rep *Report) Text(w io.Writer) {
	counts := map[Status]int{}
	for _, r := range rep.Items {
		counts[r.Status]++
	}
	fmt.Fprintf(w, "%d items: %d ok, %d warn, %d fail (block %d, price %s per chunk per block)\n",
		len(rep.Items), counts[OK], counts[Warn], counts[Fail], rep.Block, plur(rep.Price))
	list := ""
	for _, r := range rep.Items {
		if r.List != list {
			list = r.List
			fmt.Fprintf(w, "\n%s\n", list)
		}
		left := "time unknown"
		if d, ok := r.TimeLeft(); ok {
			left = About(d) + " left"
		}
		fmt.Fprintf(w, "  %-4s  %-28s  %-22s  %s\n", strings.ToUpper(string(r.Status)), r.Item, left, checked(r))
		if r.BalanceLeft != "" && r.Status != OK {
			fmt.Fprintf(w, "        batch %s: %s per chunk left\n", short(r.Batch), plur(r.BalanceLeft))
		}
		for _, p := range r.Problems {
			fmt.Fprintf(w, "        %s\n", p)
		}
		for i, m := range r.Missing {
			if i == 5 {
				fmt.Fprintf(w, "        … and %d more\n", len(r.Missing)-5)
				break
			}
			fmt.Fprintf(w, "        missing: %s\n", m)
		}
	}
}

func checked(r ItemReport) string {
	if r.Checked == 0 {
		return fmt.Sprintf("%d kept, none checked", r.Targets)
	}
	return fmt.Sprintf("%d of %d checked retrievable (%d kept)", r.Checked-len(r.Missing), r.Checked, r.Targets)
}

// Summary is one line for a notification: what needs attention, by name.
func (rep *Report) Summary() string {
	att := rep.Attention()
	if len(att) == 0 {
		return "everything kept is fine"
	}
	parts := make([]string, 0, len(att))
	for _, r := range att {
		why := "needs attention"
		switch {
		case len(r.Missing) > 0:
			why = "not retrievable"
		case r.SecondsLeft != nil:
			d, _ := r.TimeLeft()
			why = About(d) + " left"
		}
		parts = append(parts, fmt.Sprintf("%s (%s)", r.Item, why))
	}
	noun := "items need"
	if len(att) == 1 {
		noun = "item needs"
	}
	return fmt.Sprintf("%d %s attention: %s", len(att), noun, strings.Join(parts, ", "))
}
