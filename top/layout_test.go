package top

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Test_topBandLayout covers the pure geometry function that drives the verbose-aware
// top-band layout. It is gocui-free (mirrors the visibleColumns precedent): pure integer
// arithmetic over (verbose, maxY) returning the per-view y-coordinates plus an expanded flag.
//
// Compact (verbose=false OR height-guard tripped) must reproduce today's literals exactly:
// sysstatY1=4, pgstatY1=4, cmdlineY0=3, cmdlineY1=5, dbstatY0=4.
// Verbose grows sysstat by +3 (y1=7) and pgstat by +5 (y1=9); cmdline/dbstat clear the
// taller (pgstat) panel: bandTop=max(7,9)-1=8 => cmdlineY0=8, cmdlineY1=10. Both modes keep
// the table tied to the cmdline bottom (dbstatY0 = cmdlineY1 - 1), so verbose dbstatY0=9.
// Height-guard: verbose expands only when maxY can fit dbstatY0 + header(1) + >=1 data row,
// i.e. dbstatY0 + 2 <= maxY - 1 => maxY >= 12. Otherwise fall back to compact.
func Test_topBandLayout(t *testing.T) {
	testcases := []struct {
		name      string
		verbose   bool
		maxY      int
		sysstatY1 int
		pgstatY1  int
		cmdlineY0 int
		cmdlineY1 int
		dbstatY0  int
		expanded  bool
	}{
		{
			// compact: verbose off, tall terminal -> today's exact literals, not expanded.
			name: "compact", verbose: false, maxY: 50,
			sysstatY1: 4, pgstatY1: 4, cmdlineY0: 3, cmdlineY1: 5, dbstatY0: 4, expanded: false,
		},
		{
			// verbose: tall enough -> asymmetric heights, cmdline/dbstat cleared below pgstat.
			name: "verbose", verbose: true, maxY: 50,
			sysstatY1: 7, pgstatY1: 9, cmdlineY0: 8, cmdlineY1: 10, dbstatY0: 9, expanded: true,
		},
		{
			// height-guard: verbose requested on a realistically tiny terminal well below the
			// threshold -> graceful compact fallback (distinct small maxY, not the boundary value).
			name: "height-guard", verbose: true, maxY: 5,
			sysstatY1: 4, pgstatY1: 4, cmdlineY0: 3, cmdlineY1: 5, dbstatY0: 4, expanded: false,
		},
		{
			// pathological: non-positive maxY must degrade to compact, never emit inverted/negative
			// coords. layout() guards maxY==0 earlier, but the pure function must be safe regardless.
			name: "verbose-zero-maxY", verbose: true, maxY: 0,
			sysstatY1: 4, pgstatY1: 4, cmdlineY0: 3, cmdlineY1: 5, dbstatY0: 4, expanded: false,
		},
		{
			// boundary: smallest maxY that still expands (dbstatY0=9 + 2 <= maxY-1 => maxY>=12).
			name: "boundary-expands", verbose: true, maxY: 12,
			sysstatY1: 7, pgstatY1: 9, cmdlineY0: 8, cmdlineY1: 10, dbstatY0: 9, expanded: true,
		},
		{
			// boundary: largest maxY that still falls back (maxY=11 is one short of the threshold).
			name: "boundary-fallback", verbose: true, maxY: 11,
			sysstatY1: 4, pgstatY1: 4, cmdlineY0: 3, cmdlineY1: 5, dbstatY0: 4, expanded: false,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			sysstatY1, pgstatY1, cmdlineY0, cmdlineY1, dbstatY0, expanded := topBandLayout(tc.verbose, tc.maxY)
			assert.Equal(t, tc.sysstatY1, sysstatY1, "sysstatY1")
			assert.Equal(t, tc.pgstatY1, pgstatY1, "pgstatY1")
			assert.Equal(t, tc.cmdlineY0, cmdlineY0, "cmdlineY0")
			assert.Equal(t, tc.cmdlineY1, cmdlineY1, "cmdlineY1")
			assert.Equal(t, tc.dbstatY0, dbstatY0, "dbstatY0")
			assert.Equal(t, tc.expanded, expanded, "expanded")
		})
	}
}

// Test_topBandLayoutNoOverlap asserts the on-screen invariant both modes must satisfy: every
// screen row between the panels and the table belongs to some view — no row is drawn twice and
// no row is left blank. It is expressed in terms of content rows derived from the returned
// coordinates, never in terms of the expected literals, so it cannot be satisfied by repeating
// the arithmetic of topBandLayout.
//
// gocui geometry: SetView(name, x0, y0, x1, y1) yields a content area starting at (x0+1, y0+1)
// and sized (x1-x0-1) x (y1-y0-1). Frame=false (set on all four views) hides the border but
// does not change this arithmetic. So a view spanning y0..y1 owns content rows y0+1 .. y1-1.
func Test_topBandLayoutNoOverlap(t *testing.T) {
	testcases := []struct {
		name    string
		verbose bool
		maxY    int
	}{
		{name: "compact", verbose: false, maxY: 50},
		{name: "verbose", verbose: true, maxY: 50},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			sysstatY1, pgstatY1, cmdlineY0, cmdlineY1, dbstatY0, _ := topBandLayout(tc.verbose, tc.maxY)

			// cmdline spans cmdlineY0..cmdlineY1, i.e. exactly one content row.
			assert.Equal(t, 1, cmdlineY1-cmdlineY0-1, "cmdline must have exactly one content row")
			cmdlineContentRow := cmdlineY1 - 1

			// The taller panel's last content row must stay strictly above the cmdline text.
			tallestPanelY1 := sysstatY1
			if pgstatY1 > tallestPanelY1 {
				tallestPanelY1 = pgstatY1
			}
			assert.Less(t, tallestPanelY1-1, cmdlineContentRow,
				"last panel content row must be above the cmdline content row")

			// The regression assert: the table starts on the very next row after the cmdline —
			// neither overlapping it nor leaving a row nobody draws.
			dbstatFirstContentRow := dbstatY0 + 1
			assert.Equal(t, cmdlineContentRow+1, dbstatFirstContentRow,
				"dbstat must start exactly one row below the cmdline content row")

			// dbstat spans dbstatY0..maxY-1, so it needs room for at least one content row.
			assert.LessOrEqual(t, dbstatFirstContentRow, tc.maxY-2,
				"dbstat must keep at least one content row")
		})
	}
}
