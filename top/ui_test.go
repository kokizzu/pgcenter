package top

import (
	"regexp"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/lesovsky/pgcenter/internal/view"
	"github.com/stretchr/testify/assert"
)

// tokenOf is a test helper building a token from its renderings, longest first.
func tokenOf(variants ...string) cmdlineToken {
	return cmdlineToken{variants: variants}
}

// Test_composeCmdline covers the truncation ladder of the single cmdline composition point.
// The line is built as <tokens left to right><single space><message>, clamped to width RUNES.
// Ladder order: (1) the message is cut; (2) the RIGHTMOST token steps through its variants;
// (3) tokens are dropped from the right; (4) last resort, the message is hard rune-truncated.
// A token is never rendered partially: it is either whole, degraded to a whole shorter variant,
// or absent.
func Test_composeCmdline(t *testing.T) {
	// The filter token's full ladder: 19, 13 and 5 runes.
	filter := tokenOf("[F:datname,usename]", "[F:datname,…]", "[F:…]")
	// A single-variant token - the shape [PAUSED] will have - must never shrink.
	pause := tokenOf("[PAUSED]")

	testcases := []struct {
		name   string
		tokens []cmdlineToken
		msg    string
		width  int
		want   string
	}{
		{
			// no prefix at all: the line is the message, no leading separator.
			name: "no tokens, message fits",
			msg:  "Refresh: ok", width: 80, want: "Refresh: ok",
		},
		{
			// no prefix, message longer than the line: hard truncate by runes.
			name: "no tokens, message truncated",
			msg:  "Refresh: ok", width: 4, want: "Refr",
		},
		{
			// prefix and message both fit: exactly one space between them.
			name: "one token and message fit", tokens: []cmdlineToken{tokenOf("[F:datname]")},
			msg: "Refresh: ok", width: 80, want: "[F:datname] Refresh: ok",
		},
		{
			// exactly the prefix fits: message dropped whole, no trailing separator.
			name: "only prefix fits exactly", tokens: []cmdlineToken{tokenOf("[F:datname]")},
			msg: "Refresh: ok", width: 11, want: "[F:datname]",
		},
		{
			// one spare column is not enough for separator + at least one message rune:
			// the separator must not dangle at the end of the line.
			name: "one spare column, no dangling separator", tokens: []cmdlineToken{tokenOf("[F:datname]")},
			msg: "Refresh: ok", width: 12, want: "[F:datname]",
		},
		{
			// two spare columns: separator plus a single message rune.
			name: "two spare columns", tokens: []cmdlineToken{tokenOf("[F:datname]")},
			msg: "Refresh: ok", width: 13, want: "[F:datname] R",
		},
		{
			// the message is cut before the prefix is touched: the prefix stays at its longest
			// variant even though degrading it would have let the whole message through.
			name: "message cut before prefix", tokens: []cmdlineToken{filter},
			msg: "Refresh: ok", width: 22, want: "[F:datname,usename] Re",
		},
		{
			// prefix does not fit: the rightmost token steps to its second variant.
			name: "rightmost token degrades one step", tokens: []cmdlineToken{filter},
			msg: "", width: 15, want: "[F:datname,…]",
		},
		{
			// still does not fit: the token steps to its shortest variant.
			name: "rightmost token degrades to shortest", tokens: []cmdlineToken{filter},
			msg: "", width: 6, want: "[F:…]",
		},
		{
			// variants exhausted: the token is dropped from the right, the message survives.
			name: "variants exhausted, token dropped", tokens: []cmdlineToken{filter},
			msg: "hello", width: 4, want: "hell",
		},
		{
			// two tokens: the rightmost degrades while the single-variant left one stays whole.
			name: "left token stays whole while right degrades", tokens: []cmdlineToken{pause, filter},
			msg: "", width: 21, want: "[PAUSED][F:datname,…]",
		},
		{
			// two tokens: the right one is dropped entirely, the left one still fits.
			name: "right token dropped, left kept", tokens: []cmdlineToken{pause, filter},
			msg: "", width: 12, want: "[PAUSED]",
		},
		{
			// a single-variant token is never rendered partially: it is dropped instead.
			name: "single variant token never shrinks", tokens: []cmdlineToken{pause},
			msg: "hello", width: 7, want: "hello",
		},
		{
			// ... and it is rendered whole as soon as it fits.
			name: "single variant token fits whole", tokens: []cmdlineToken{pause},
			msg: "", width: 8, want: "[PAUSED]",
		},
		{
			// degenerate view geometry must not panic and must not emit anything.
			name: "zero width", tokens: []cmdlineToken{filter}, msg: "Refresh: ok", width: 0, want: "",
		},
		{
			name: "negative width", tokens: []cmdlineToken{filter}, msg: "Refresh: ok", width: -5, want: "",
		},
		{
			// no tokens and no message: an empty line, not a stray separator.
			name: "nothing to render", msg: "", width: 80, want: "",
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, composeCmdline(tc.tokens, tc.msg, tc.width))
		})
	}
}

// Test_composeCmdlineTwoTokens is the user-spec acceptance criterion: the composer accepts a
// second token - the [PAUSED] one a later feature will add - and assembles the line without the
// function itself being edited. The pause token is built here as a literal; no pause code exists
// in this feature and none must appear.
func Test_composeCmdlineTwoTokens(t *testing.T) {
	pause := cmdlineToken{variants: []string{"[PAUSED]"}}

	filter, ok := filterToken(
		map[int]*regexp.Regexp{0: regexp.MustCompile("^pgcenter")},
		[]string{"datname", "usename"},
	)
	assert.True(t, ok)

	assert.Equal(t,
		"[PAUSED][F:datname] сообщение",
		composeCmdline([]cmdlineToken{pause, filter}, "сообщение", 80),
	)
}

// Test_composeCmdlineRunes pins Decision 12: every width computation counts runes, not bytes.
// Each case has a byte length above the width and a rune length within it, so a len()-based
// implementation renders something different.
func Test_composeCmdlineRunes(t *testing.T) {
	// 9 runes / 18 bytes, width 10: byte arithmetic would truncate it.
	assert.Equal(t, "сообщение", composeCmdline(nil, "сообщение", 10))

	// The ellipsis is 1 column and 3 bytes: "[F:…]" is 5 runes / 7 bytes, so with a 2-rune
	// message and a separator the line is exactly 8 columns wide.
	assert.Equal(t, "[F:…] ok", composeCmdline([]cmdlineToken{tokenOf("[F:…]")}, "ok", 8))

	// Truncation itself is by runes and never cuts a rune in half.
	got := composeCmdline(nil, "привет мир", 6)
	assert.Equal(t, "привет", got)
	assert.True(t, utf8.ValidString(got))
}

// Test_filterToken covers building the [F:...] token out of a view's filters and the column
// names known at that moment.
func Test_filterToken(t *testing.T) {
	re := regexp.MustCompile("^a")
	cols := []string{"datname", "usename", "state"}

	testcases := []struct {
		name     string
		filters  map[int]*regexp.Regexp
		cols     []string
		wantOK   bool
		variants []string
	}{
		{
			name: "no filters at all", filters: map[int]*regexp.Regexp{}, cols: cols, wantOK: false,
		},
		{
			name: "nil filters map", filters: nil, cols: cols, wantOK: false,
		},
		{
			// the predicate must match printHeaderCell's '*' marker, not the weaker nil-only one.
			name:    "nil regexp is not an active filter",
			filters: map[int]*regexp.Regexp{0: nil}, cols: cols, wantOK: false,
		},
		{
			// same: an empty pattern draws no '*' in the header, so it must draw no indicator.
			name:    "empty pattern is not an active filter",
			filters: map[int]*regexp.Regexp{0: regexp.MustCompile("")}, cols: cols, wantOK: false,
		},
		{
			// a single name has no honest middle variant: "[F:datname,…]" would claim a second filter.
			name:    "single filter",
			filters: map[int]*regexp.Regexp{0: re}, cols: cols, wantOK: true,
			variants: []string{"[F:datname]", "[F:…]"},
		},
		{
			// names ordered by ascending column index, which is also left-to-right screen order.
			name:    "several filters",
			filters: map[int]*regexp.Regexp{2: re, 0: re}, cols: cols, wantOK: true,
			variants: []string{"[F:datname,state]", "[F:datname,…]", "[F:…]"},
		},
		{
			// first frame: Cols is filled by the first successful render, not by view.New().
			name:    "nil cols",
			filters: map[int]*regexp.Regexp{0: re}, cols: nil, wantOK: false,
		},
		{
			// one frame after a screen switch the column count can lag the filters (issue #99).
			name:    "index out of range is skipped",
			filters: map[int]*regexp.Regexp{0: re, 7: re}, cols: cols, wantOK: true,
			variants: []string{"[F:datname]", "[F:…]"},
		},
		{
			name:    "negative index is skipped",
			filters: map[int]*regexp.Regexp{-1: re, 1: re}, cols: cols, wantOK: true,
			variants: []string{"[F:usename]", "[F:…]"},
		},
		{
			// an empty "[F:]" would claim a nameless filter - worse than no indicator at all.
			name:    "all indexes out of range",
			filters: map[int]*regexp.Regexp{7: re, 9: re}, cols: cols, wantOK: false,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := filterToken(tc.filters, tc.cols)
			assert.Equal(t, tc.wantOK, ok)
			if !tc.wantOK {
				assert.Empty(t, got.variants)
				return
			}
			assert.Equal(t, tc.variants, got.variants)
		})
	}
}

// Test_filterTokenDeterministicOrder pins Decision 6. Go randomises map iteration, so an
// unsorted implementation is green on a single pass roughly as often as not - the loop is the
// substance of this check. Keys are inserted in a non-ascending order so insertion order differs
// from the expected one.
func Test_filterTokenDeterministicOrder(t *testing.T) {
	re := regexp.MustCompile("^a")
	cols := []string{"datname", "usename", "state", "waiting"}

	for i := 0; i < 50; i++ {
		filters := map[int]*regexp.Regexp{}
		filters[3] = re
		filters[1] = re
		filters[0] = re
		filters[2] = re

		got, ok := filterToken(filters, cols)
		assert.True(t, ok)
		assert.Equal(t, "[F:datname,usename,state,waiting]", got.variants[0])
		assert.Equal(t, "[F:datname,…]", got.variants[1])
	}
}

// Test_filterTokenStripsControlRunes pins Decision 13. Column names come from the server's row
// description, and the cmdline emits no SGR of its own to heal an unterminated sequence. Control
// runes are what gets removed - the printable tail "[31m" is ordinary text and stays.
func Test_filterTokenStripsControlRunes(t *testing.T) {
	re := regexp.MustCompile("^a")

	t.Run("escape sequence loses its control rune only", func(t *testing.T) {
		got, ok := filterToken(map[int]*regexp.Regexp{0: re}, []string{"dat\033[31mname"})
		assert.True(t, ok)
		assert.Equal(t, "[F:dat[31mname]", got.variants[0])

		for _, r := range got.variants[0] {
			assert.False(t, unicode.IsControl(r), "control rune %q left in the token", r)
		}
		// The ladder's arithmetic depends on the rune count matching the visible width.
		assert.Equal(t, len("[F:dat[31mname]"), utf8.RuneCountInString(got.variants[0]))
	})

	t.Run("other control runes are removed too", func(t *testing.T) {
		got, ok := filterToken(map[int]*regexp.Regexp{0: re}, []string{"dat\rna\x00me"})
		assert.True(t, ok)
		assert.Equal(t, "[F:datname]", got.variants[0])
	})

	t.Run("name of control runes only is dropped", func(t *testing.T) {
		got, ok := filterToken(map[int]*regexp.Regexp{0: re}, []string{"\r\n\x00"})
		assert.False(t, ok)
		assert.Empty(t, got.variants)
	})

	t.Run("blanked name does not blank its neighbours", func(t *testing.T) {
		got, ok := filterToken(
			map[int]*regexp.Regexp{0: re, 1: re},
			[]string{"\x00\r", "use\x01name"},
		)
		assert.True(t, ok)
		assert.Equal(t, "[F:usename]", got.variants[0])
		assert.False(t, strings.Contains(got.variants[0], ","))
	})
}

// Test_cmdlineTokens covers reading the state tokens off a config. It must be nil-safe: unit
// tests hand it a bare config and the ambient can be empty.
func Test_cmdlineTokens(t *testing.T) {
	t.Run("nil config", func(t *testing.T) {
		assert.Empty(t, cmdlineTokens(nil))
	})

	t.Run("bare config", func(t *testing.T) {
		assert.Empty(t, cmdlineTokens(newConfig()))
	})

	t.Run("config with active filters", func(t *testing.T) {
		re := regexp.MustCompile("^a")
		c := newConfig()
		c.view = view.View{
			Cols:    []string{"datname", "usename"},
			Filters: map[int]*regexp.Regexp{1: re},
		}

		want, ok := filterToken(c.view.Filters, c.view.Cols)
		assert.True(t, ok)

		got := cmdlineTokens(c)
		assert.Equal(t, []cmdlineToken{want}, got)
	})
}

// Test_setCmdlineConfig checks the ambient is published by the named setter and read back by
// cmdlineTokens. The previous value is restored - leaving a test's config in a package-level
// pointer is exactly what Decision 1 forbids by keeping the setter out of newApp.
func Test_setCmdlineConfig(t *testing.T) {
	prev := cmdlineCfg
	t.Cleanup(func() { cmdlineCfg = prev })

	c := newConfig()
	c.view = view.View{
		Cols:    []string{"datname"},
		Filters: map[int]*regexp.Regexp{0: regexp.MustCompile("^a")},
	}

	setCmdlineConfig(c)
	assert.Same(t, c, cmdlineCfg)

	tokens := cmdlineTokens(cmdlineCfg)
	assert.Len(t, tokens, 1)
	assert.Equal(t, "[F:datname]", tokens[0].variants[0])
}

// Test_printCmdlineNilGui covers the only part of the writers a unit test can reach: gocui.View
// cannot be constructed here. Both writers must return silently on a nil Gui without touching
// the ambient - a nil ambient is left in place on purpose to catch a deref before g.Update.
func Test_printCmdlineNilGui(t *testing.T) {
	prev := cmdlineCfg
	t.Cleanup(func() { cmdlineCfg = prev })
	cmdlineCfg = nil

	assert.NotPanics(t, func() { printCmdline(nil, "%s", "message") })
	assert.NotPanics(t, func() { printCmdlinePersist(nil, "%s", "prompt") })
	assert.NotPanics(t, func() { printCmdline(nil, "") })
}
