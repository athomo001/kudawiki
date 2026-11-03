package main

import (
	"flag"
	"slices"
	"testing"
)

// Los flags se leen antes o después del subcomando; los que no son de este FlagSet quedan para
// el subcomando.
func TestParseInterleaved(t *testing.T) {
	fs := flag.NewFlagSet("x", flag.ContinueOnError)
	dry := fs.Bool("dry-run", false, "")
	from := fs.String("from", "", "")
	to := fs.String("to", "", "")
	got := parseInterleaved(fs, []string{"export", "--from", "2026-01-01", "--to=2026-02-01", "--dry-run", "split", "--shares", "3", "--all"})
	if !*dry || *from != "2026-01-01" || *to != "2026-02-01" {
		t.Errorf("flags: dry=%v from=%q to=%q", *dry, *from, *to)
	}
	if want := []string{"export", "split", "--shares", "3", "--all"}; !slices.Equal(got, want) {
		t.Errorf("positional = %q, want %q", got, want)
	}
}
