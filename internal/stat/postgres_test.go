package stat

import (
	"archive/tar"
	"bytes"
	"database/sql"
	"fmt"
	"github.com/lesovsky/pgcenter/internal/postgres"
	"github.com/lesovsky/pgcenter/internal/query"
	"github.com/stretchr/testify/assert"
	"io"
	"os"
	"testing"
)

// newTestPGresult return PGresult with test content for test purposes.
func newTestPGresult() PGresult {
	return PGresult{
		Valid: true,
		Ncols: 4,
		Nrows: 8,
		Cols:  []string{"col1", "col2", "col3", "col4"},
		Values: [][]sql.NullString{
			{
				{String: "248", Valid: true}, {String: "brodsky", Valid: true}, {String: "row6:value3", Valid: true}, {String: "row6:value4", Valid: true},
			},
			{
				{String: "3", Valid: true}, {String: "direct", Valid: true}, {String: "row3:value3", Valid: true}, {String: "row3:value4", Valid: true},
			},
			{
				{String: "15", Valid: true}, {String: "evioni", Valid: true}, {String: "row5:value3", Valid: true}, {String: "row2:value4", Valid: true},
			},
			{
				{String: "48752", Valid: true}, {String: "aalfia", Valid: true}, {String: "row8:value3", Valid: true}, {String: "row8:value4", Valid: true},
			},
			{
				{String: "2", Valid: true}, {String: "cilla", Valid: true}, {String: "row2:value3", Valid: true}, {String: "row2:value4", Valid: true},
			},
			{
				{String: "4", Valid: true}, {String: "arktika", Valid: true}, {String: "row3:value3", Valid: true}, {String: "row4:value4", Valid: true},
			},
			{
				{String: "3987", Valid: true}, {String: "fasivy", Valid: true}, {String: "row7:value3", Valid: true}, {String: "row7:value4", Valid: true},
			},
			{
				{String: "1", Valid: true}, {String: "bronze", Valid: true}, {String: "row1:value3", Valid: true}, {String: "row1:value4", Valid: true},
			},
		},
	}
}

func Test_collectPostgresStat(t *testing.T) {
	conn, err := postgres.NewTestConnect()
	assert.NoError(t, err)

	got, err := collectPostgresStat(conn, query.PgStatDatabaseGeneralDefault)
	assert.NoError(t, err)
	assert.Greater(t, got.Nrows, 0)

	// testing with already closed conn
	conn.Close()
	_, err = collectPostgresStat(conn, "SELECT qq")
	assert.Error(t, err)
}

func Test_collectActivityStat(t *testing.T) {
	conn, err := postgres.NewTestConnect()
	assert.NoError(t, err)

	version := 1000000 // suppose to use PG 100.0
	got, err := collectActivityStat(conn, version, "public", 1, 0)
	assert.NoError(t, err)
	assert.Equal(t, "ok", got.State)
	assert.NotEqual(t, "", got.Uptime)
	assert.NotEqual(t, "", got.Recovery)
	assert.NotEqual(t, "", got.Recovery)
	assert.Greater(t, got.ConnTotal+got.ConnIdle+got.ConnIdleXact+got.ConnActive+got.ConnWaiting+got.ConnOthers+got.ConnPrepared, 0)
	assert.NotEqual(t, 0, got.StmtAvgTime)
	assert.NotEqual(t, 0, got.Calls)
	assert.NotEqual(t, 0, got.CallsRate)

	// testing with already closed conn
	conn.Close()
	_, err = collectActivityStat(conn, 0, "public", 1, 0)
	assert.Error(t, err)
}

func TestGetPostgresProperties(t *testing.T) {
	conn, err := postgres.NewTestConnect()
	assert.NoError(t, err)

	conn.Local = false // set conn as non-local

	got, err := GetPostgresProperties(conn)
	assert.NoError(t, err)
	assert.NotEqual(t, "", got.Version)
	assert.NotEqual(t, 0, got.VersionNum)
	assert.NotEqual(t, "", got.GucTrackCommitTimestamp)
	assert.NotEqual(t, 0, got.GucMaxConnections)
	assert.NotEqual(t, 0, got.GucAVMaxWorkers)
	assert.NotEqual(t, "", got.Recovery)
	assert.NotEqual(t, "", got.StartTime)
	assert.NotEqual(t, 0, got.SysTicks)

	// testing with already closed conn
	conn.Close()
	_, err = GetPostgresProperties(conn)
	assert.Error(t, err)
}

func TestNewPGresultQuery(t *testing.T) {
	conn, err := postgres.NewTestConnect()
	assert.NoError(t, err)

	want := PGresult{
		Valid: true, Ncols: 4, Nrows: 3, Cols: []string{"id", "name", "v1", "v2"},
		Values: [][]sql.NullString{
			{{String: "1", Valid: true}, {String: "one", Valid: true}, {String: "10", Valid: true}, {String: "111e-1", Valid: true}},
			{{String: "2", Valid: true}, {String: "two", Valid: true}, {String: "20", Valid: true}, {String: "222e-1", Valid: true}},
			// next row contains NULL values, all Valid fields are 'false'
			{{String: "3", Valid: true}, {String: "", Valid: false}, {String: "", Valid: false}, {String: "", Valid: false}},
		},
	}
	got, err := NewPGresultQuery(conn, "SELECT * FROM (VALUES (1,'one',10,11.1), (2,'two',20,22.2), (3,NULL,NULL,NULL)) AS t (id,name,v1,v2)")
	assert.NoError(t, err)
	assert.Equal(t, want, got)

	// testing empty query
	_, err = NewPGresultQuery(conn, "")
	assert.Error(t, err)

	// testing with already closed conn
	conn.Close()
	_, err = NewPGresultQuery(conn, "SELECT 1")
	assert.Error(t, err)
}

func Test_NewPGresultFile(t *testing.T) {
	testcases := []struct {
		valid    bool
		filename string
	}{
		{valid: true, filename: "testdata/pgcenter.stat.golden.tar"},
		{valid: false, filename: "testdata/pgcenter.stat.invalid.tar"},
	}

	for _, tc := range testcases {
		t.Run(tc.filename, func(t *testing.T) {
			f, err := os.Open(tc.filename)
			assert.NoError(t, err)

			r := tar.NewReader(f)

			for {
				hdr, err := r.Next()
				if err == io.EOF {
					break
				} else if err != nil {
					assert.Fail(t, "unexpected error", err)
				}

				got, err := NewPGresultFile(r, hdr.Size)
				if tc.valid {
					assert.NoError(t, err)
					assert.NotNil(t, got.Values)
					assert.NotNil(t, got.Cols)
				} else {
					assert.Error(t, err)
					assert.Equal(t, PGresult{}, got)
				}
			}
		})
	}
}

func TestPGresult_validate(t *testing.T) {
	testcases := []struct {
		valid bool
		res   PGresult
	}{
		{valid: true, res: PGresult{
			Valid: true, Ncols: 4, Nrows: 2, Cols: []string{"col1", "col2", "col3", "col4"},
			Values: [][]sql.NullString{
				{{String: "1", Valid: true}, {String: "one", Valid: true}, {String: "10", Valid: true}, {String: "111e-1", Valid: true}},
				{{String: "3", Valid: true}, {String: "", Valid: false}, {String: "", Valid: false}, {String: "", Valid: false}},
			},
		}},
		{valid: false, res: PGresult{
			Valid: true, Ncols: 4, Nrows: 1, Cols: []string{"col1", "col2", "col3", "col4"},
			Values: [][]sql.NullString{
				{{String: "1", Valid: true}, {String: "one", Valid: true}, {String: "10", Valid: true}},
			},
		}},
		{valid: false, res: PGresult{
			Valid: true, Ncols: 4, Nrows: 2, Cols: []string{"col1", "col2", "col3", "col4"},
			Values: [][]sql.NullString{
				{{String: "1", Valid: true}, {String: "one", Valid: true}, {String: "10", Valid: true}, {String: "111e-1", Valid: true}},
			},
		}},
	}

	for _, tc := range testcases {
		err := tc.res.validate()
		if tc.valid {
			assert.NoError(t, err)
		} else {
			assert.Error(t, err)
		}
	}
}

func Test_calculateDelta(t *testing.T) {
	prev := PGresult{
		Valid: true, Ncols: 4, Nrows: 4, Cols: []string{"unique", "col2", "col3", "col4"},
		Values: [][]sql.NullString{
			{{String: "1", Valid: true}, {String: "300", Valid: true}, {String: "100", Valid: true}, {String: "500", Valid: true}},
			{{String: "2", Valid: true}, {String: "400", Valid: true}, {String: "200", Valid: true}, {String: "600", Valid: true}},
			{{String: "3", Valid: true}, {String: "100.0", Valid: true}, {String: "300", Valid: true}, {String: "700", Valid: true}},
			{{String: "4", Valid: true}, {String: "200", Valid: true}, {String: "400.0", Valid: true}, {String: "800", Valid: true}},
			// next row is not present in 'curr' and should be skipped.
			{{String: "5", Valid: true}, {String: "200", Valid: true}, {String: "400.0", Valid: true}, {String: "800", Valid: true}},
		},
	}
	curr := PGresult{
		Valid: true, Ncols: 4, Nrows: 5, Cols: []string{"unique", "col2", "col3", "col4"},
		Values: [][]sql.NullString{
			{{String: "1", Valid: true}, {String: "330.5", Valid: true}, {String: "150", Valid: true}, {String: "500", Valid: true}},
			{{String: "2", Valid: true}, {String: "440", Valid: true}, {String: "280.6", Valid: true}, {String: "620", Valid: true}},
			{{String: "3", Valid: true}, {String: "110", Valid: true}, {String: "300", Valid: true}, {String: "710", Valid: true}},
			{{String: "4", Valid: true}, {String: "220", Valid: true}, {String: "490", Valid: true}, {String: "800", Valid: true}},
			// next row is not present in 'prev' and should be added as-is to 'diff' result.
			{{String: "6", Valid: true}, {String: "560", Valid: true}, {String: "510", Valid: true}, {String: "920", Valid: true}},
		},
	}
	currInvalid := PGresult{
		Valid: true, Ncols: 4, Nrows: 1, Cols: []string{"unique", "col2", "col3", "col4"},
		Values: [][]sql.NullString{
			{{String: "1", Valid: true}, {String: "invalid", Valid: true}, {String: "150", Valid: true}, {String: "500", Valid: true}},
		},
	}
	wantAsc := PGresult{
		Valid: true, Ncols: 4, Nrows: 5, Cols: []string{"unique", "col2", "col3", "col4"},
		Values: [][]sql.NullString{
			{{String: "3", Valid: true}, {String: "10.00", Valid: true}, {String: "0", Valid: true}, {String: "10", Valid: true}},
			{{String: "4", Valid: true}, {String: "20", Valid: true}, {String: "90.00", Valid: true}, {String: "0", Valid: true}},
			{{String: "1", Valid: true}, {String: "30.50", Valid: true}, {String: "50", Valid: true}, {String: "0", Valid: true}},
			{{String: "2", Valid: true}, {String: "40", Valid: true}, {String: "80.60", Valid: true}, {String: "20", Valid: true}},
			{{String: "6", Valid: true}, {String: "560", Valid: true}, {String: "510", Valid: true}, {String: "920", Valid: true}},
		},
	}
	wantDesc := PGresult{
		Valid: true, Ncols: 4, Nrows: 5, Cols: []string{"unique", "col2", "col3", "col4"},
		Values: [][]sql.NullString{
			{{String: "6", Valid: true}, {String: "560", Valid: true}, {String: "510", Valid: true}, {String: "920", Valid: true}},
			{{String: "2", Valid: true}, {String: "40", Valid: true}, {String: "80.60", Valid: true}, {String: "20", Valid: true}},
			{{String: "1", Valid: true}, {String: "30.50", Valid: true}, {String: "50", Valid: true}, {String: "0", Valid: true}},
			{{String: "4", Valid: true}, {String: "20", Valid: true}, {String: "90.00", Valid: true}, {String: "0", Valid: true}},
			{{String: "3", Valid: true}, {String: "10.00", Valid: true}, {String: "0", Valid: true}, {String: "10", Valid: true}},
		},
	}

	// calculate delta with ASC sort
	got, err := calculateDelta(curr, prev, 1, [2]int{1, 3}, 1, false, 0)
	assert.NoError(t, err)
	assert.Equal(t, wantAsc, got)

	// calculate delta with DESC sort
	got, err = calculateDelta(curr, prev, 1, [2]int{1, 3}, 1, true, 0)
	assert.NoError(t, err)
	assert.Equal(t, wantDesc, got)

	// calculate delta with zero diff-interval, just return current value
	got, err = calculateDelta(curr, prev, 1, [2]int{0, 0}, 1, true, 0)
	assert.NoError(t, err)
	assert.Equal(t, curr, got)

	// calculate with invalid input data
	_, err = calculateDelta(currInvalid, prev, 1, [2]int{1, 3}, 1, true, 0)
	assert.Error(t, err)
}

func Test_diff(t *testing.T) {
	prev := PGresult{
		Valid: true, Ncols: 4, Nrows: 4, Cols: []string{"unique", "col2", "col3", "col4"},
		Values: [][]sql.NullString{
			{{String: "1", Valid: true}, {String: "300", Valid: true}, {String: "100", Valid: true}, {String: "500", Valid: true}},
			{{String: "2", Valid: true}, {String: "400", Valid: true}, {String: "200", Valid: true}, {String: "600", Valid: true}},
			{{String: "3", Valid: true}, {String: "100.0", Valid: true}, {String: "300", Valid: true}, {String: "700", Valid: true}},
			{{String: "4", Valid: true}, {String: "200", Valid: true}, {String: "400.0", Valid: true}, {String: "800", Valid: true}},
			// next row is not present in 'curr' and should be skipped.
			{{String: "5", Valid: true}, {String: "200", Valid: true}, {String: "400.0", Valid: true}, {String: "800", Valid: true}},
		},
	}
	curr := PGresult{
		Valid: true, Ncols: 4, Nrows: 5, Cols: []string{"unique", "col2", "col3", "col4"},
		Values: [][]sql.NullString{
			{{String: "1", Valid: true}, {String: "330.5", Valid: true}, {String: "150", Valid: true}, {String: "500", Valid: true}},
			{{String: "2", Valid: true}, {String: "440", Valid: true}, {String: "280.6", Valid: true}, {String: "620", Valid: true}},
			{{String: "3", Valid: true}, {String: "110", Valid: true}, {String: "300", Valid: true}, {String: "710", Valid: true}},
			{{String: "4", Valid: true}, {String: "220", Valid: true}, {String: "490", Valid: true}, {String: "800", Valid: true}},
			// next row is not present in 'prev' and should be added as-is to 'diff' result.
			{{String: "6", Valid: true}, {String: "560", Valid: true}, {String: "510", Valid: true}, {String: "920", Valid: true}},
		},
	}
	want := PGresult{
		Valid: true, Ncols: 4, Nrows: 5, Cols: []string{"unique", "col2", "col3", "col4"},
		Values: [][]sql.NullString{
			{{String: "1", Valid: true}, {String: "30.50", Valid: true}, {String: "50", Valid: true}, {String: "0", Valid: true}},
			{{String: "2", Valid: true}, {String: "40", Valid: true}, {String: "80.60", Valid: true}, {String: "20", Valid: true}},
			{{String: "3", Valid: true}, {String: "10.00", Valid: true}, {String: "0", Valid: true}, {String: "10", Valid: true}},
			{{String: "4", Valid: true}, {String: "20", Valid: true}, {String: "90.00", Valid: true}, {String: "0", Valid: true}},
			{{String: "6", Valid: true}, {String: "560", Valid: true}, {String: "510", Valid: true}, {String: "920", Valid: true}},
		},
	}

	got, err := diff(curr, prev, 1, [2]int{1, 3}, 0)
	assert.NoError(t, err)
	assert.Equal(t, want, got)

	prevValid := PGresult{
		Valid: true, Ncols: 2, Nrows: 1, Cols: []string{"unique", "col2"},
		Values: [][]sql.NullString{
			{{String: "1", Valid: true}, {String: "300", Valid: true}},
		},
	}
	currInvalid := PGresult{
		Valid: true, Ncols: 2, Nrows: 1, Cols: []string{"unique", "col2"},
		Values: [][]sql.NullString{
			{{String: "1", Valid: true}, {String: "invalid", Valid: true}},
		},
	}

	_, err = diff(currInvalid, prevValid, 1, [2]int{1, 3}, 0)
	assert.Error(t, err)
}

func Test_sort(t *testing.T) {
	res := newTestPGresult()
	testcases := []struct {
		name string
		key  int
		desc bool
		want [][]sql.NullString
	}{
		{
			name: "numeric asc", key: 0, desc: false,
			want: [][]sql.NullString{
				{{String: "1", Valid: true}, {String: "bronze", Valid: true}, {String: "row1:value3", Valid: true}, {String: "row1:value4", Valid: true}},
				{{String: "2", Valid: true}, {String: "cilla", Valid: true}, {String: "row2:value3", Valid: true}, {String: "row2:value4", Valid: true}},
				{{String: "3", Valid: true}, {String: "direct", Valid: true}, {String: "row3:value3", Valid: true}, {String: "row3:value4", Valid: true}},
				{{String: "4", Valid: true}, {String: "arktika", Valid: true}, {String: "row3:value3", Valid: true}, {String: "row4:value4", Valid: true}},
				{{String: "15", Valid: true}, {String: "evioni", Valid: true}, {String: "row5:value3", Valid: true}, {String: "row2:value4", Valid: true}},
				{{String: "248", Valid: true}, {String: "brodsky", Valid: true}, {String: "row6:value3", Valid: true}, {String: "row6:value4", Valid: true}},
				{{String: "3987", Valid: true}, {String: "fasivy", Valid: true}, {String: "row7:value3", Valid: true}, {String: "row7:value4", Valid: true}},
				{{String: "48752", Valid: true}, {String: "aalfia", Valid: true}, {String: "row8:value3", Valid: true}, {String: "row8:value4", Valid: true}},
			},
		},
		{
			name: "numeric desc", key: 0, desc: true,
			want: [][]sql.NullString{
				{{String: "48752", Valid: true}, {String: "aalfia", Valid: true}, {String: "row8:value3", Valid: true}, {String: "row8:value4", Valid: true}},
				{{String: "3987", Valid: true}, {String: "fasivy", Valid: true}, {String: "row7:value3", Valid: true}, {String: "row7:value4", Valid: true}},
				{{String: "248", Valid: true}, {String: "brodsky", Valid: true}, {String: "row6:value3", Valid: true}, {String: "row6:value4", Valid: true}},
				{{String: "15", Valid: true}, {String: "evioni", Valid: true}, {String: "row5:value3", Valid: true}, {String: "row2:value4", Valid: true}},
				{{String: "4", Valid: true}, {String: "arktika", Valid: true}, {String: "row3:value3", Valid: true}, {String: "row4:value4", Valid: true}},
				{{String: "3", Valid: true}, {String: "direct", Valid: true}, {String: "row3:value3", Valid: true}, {String: "row3:value4", Valid: true}},
				{{String: "2", Valid: true}, {String: "cilla", Valid: true}, {String: "row2:value3", Valid: true}, {String: "row2:value4", Valid: true}},
				{{String: "1", Valid: true}, {String: "bronze", Valid: true}, {String: "row1:value3", Valid: true}, {String: "row1:value4", Valid: true}},
			},
		},
		{
			name: "string asc", key: 1, desc: false,
			want: [][]sql.NullString{
				{{String: "48752", Valid: true}, {String: "aalfia", Valid: true}, {String: "row8:value3", Valid: true}, {String: "row8:value4", Valid: true}},
				{{String: "4", Valid: true}, {String: "arktika", Valid: true}, {String: "row3:value3", Valid: true}, {String: "row4:value4", Valid: true}},
				{{String: "248", Valid: true}, {String: "brodsky", Valid: true}, {String: "row6:value3", Valid: true}, {String: "row6:value4", Valid: true}},
				{{String: "1", Valid: true}, {String: "bronze", Valid: true}, {String: "row1:value3", Valid: true}, {String: "row1:value4", Valid: true}},
				{{String: "2", Valid: true}, {String: "cilla", Valid: true}, {String: "row2:value3", Valid: true}, {String: "row2:value4", Valid: true}},
				{{String: "3", Valid: true}, {String: "direct", Valid: true}, {String: "row3:value3", Valid: true}, {String: "row3:value4", Valid: true}},
				{{String: "15", Valid: true}, {String: "evioni", Valid: true}, {String: "row5:value3", Valid: true}, {String: "row2:value4", Valid: true}},
				{{String: "3987", Valid: true}, {String: "fasivy", Valid: true}, {String: "row7:value3", Valid: true}, {String: "row7:value4", Valid: true}},
			},
		},
		{
			name: "string desc", key: 1, desc: true,
			want: [][]sql.NullString{
				{{String: "3987", Valid: true}, {String: "fasivy", Valid: true}, {String: "row7:value3", Valid: true}, {String: "row7:value4", Valid: true}},
				{{String: "15", Valid: true}, {String: "evioni", Valid: true}, {String: "row5:value3", Valid: true}, {String: "row2:value4", Valid: true}},
				{{String: "3", Valid: true}, {String: "direct", Valid: true}, {String: "row3:value3", Valid: true}, {String: "row3:value4", Valid: true}},
				{{String: "2", Valid: true}, {String: "cilla", Valid: true}, {String: "row2:value3", Valid: true}, {String: "row2:value4", Valid: true}},
				{{String: "1", Valid: true}, {String: "bronze", Valid: true}, {String: "row1:value3", Valid: true}, {String: "row1:value4", Valid: true}},
				{{String: "248", Valid: true}, {String: "brodsky", Valid: true}, {String: "row6:value3", Valid: true}, {String: "row6:value4", Valid: true}},
				{{String: "4", Valid: true}, {String: "arktika", Valid: true}, {String: "row3:value3", Valid: true}, {String: "row4:value4", Valid: true}},
				{{String: "48752", Valid: true}, {String: "aalfia", Valid: true}, {String: "row8:value3", Valid: true}, {String: "row8:value4", Valid: true}},
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			res.sort(tc.key, tc.desc)
			assert.Equal(t, tc.want, res.Values)
		})
	}

	// test sorting of empty PGresult.
	emptyRes := PGresult{Valid: true, Ncols: 1, Nrows: 0, Cols: []string{"col1"}, Values: [][]sql.NullString{}}
	emptyRes.sort(0, false)
	assert.Equal(t, emptyRes.Values, [][]sql.NullString{})
}

func Test_diffPair(t *testing.T) {
	testcases := []struct {
		valid bool
		curr  string
		prev  string
		want  string
	}{
		{valid: true, curr: "100", prev: "10", want: "90"},
		{valid: false, curr: "100", prev: ""},
		{valid: true, curr: "100", prev: "55.55", want: "44.45"},
		{valid: true, curr: "44.45", prev: "0", want: "44.45"},
		{valid: true, curr: "1.23456e+05", prev: "100000", want: "23456.00"},
		{valid: true, curr: "100000", prev: "1.23456e+05", want: "-23456.00"},
		{valid: false, curr: "invalid", prev: "1.23456e+05"},
	}

	for _, tc := range testcases {
		got, err := diffPair(tc.curr, tc.prev, 1)
		if tc.valid {
			assert.NoError(t, err)
			assert.Equal(t, tc.want, got)
		} else {
			assert.Error(t, err)
		}
	}
}

func Test_parsePairFloat(t *testing.T) {
	testcases := []struct {
		valid bool
		curr  string
		prev  string
		c     float64
		p     float64
	}{
		{valid: true, curr: "123.456", prev: "654.321", c: 123.456, p: 654.321},
		{valid: true, curr: "1.23456e+05", prev: "6.54321e-01", c: 123456, p: 0.654321},
		{valid: false, curr: "123.456", prev: "invalid"},
		{valid: false, curr: "invalid", prev: "123.456"},
		{valid: false, curr: "123.456", prev: ""},
		{valid: false, curr: "", prev: "123.456"},
	}

	for _, tc := range testcases {
		c, p, err := parsePairFloat(tc.curr, tc.prev)
		if tc.valid {
			assert.NoError(t, err)
			assert.Equal(t, tc.c, c)
			assert.Equal(t, tc.p, p)
		} else {
			assert.Error(t, err)
		}
	}
}

func Test_parsePairInt(t *testing.T) {
	testcases := []struct {
		valid bool
		curr  string
		prev  string
		c     int64
		p     int64
	}{
		{valid: true, curr: "123456", prev: "654321", c: 123456, p: 654321},
		{valid: false, curr: "123456", prev: "invalid"},
		{valid: false, curr: "invalid", prev: "123456"},
		{valid: false, curr: "123456", prev: ""},
		{valid: false, curr: "", prev: "123456"},
	}

	for _, tc := range testcases {
		c, p, err := parsePairInt(tc.curr, tc.prev)
		if tc.valid {
			assert.NoError(t, err)
			assert.Equal(t, tc.c, c)
			assert.Equal(t, tc.p, p)
		} else {
			assert.Error(t, err)
		}
	}
}

func TestPGresult_Fprint(t *testing.T) {
	res := newTestPGresult()

	var buf bytes.Buffer
	err := res.Fprint(&buf)
	assert.NoError(t, err)
	assert.Greater(t, len(buf.String()), 0)
	for i := 1; i <= res.Ncols; i++ {
		assert.Contains(t, buf.String(), fmt.Sprintf("row%d:value4", i))
	}
}

func Test_extensionSchema(t *testing.T) {
	conn, err := postgres.NewTestConnect()
	assert.NoError(t, err)

	// test with proper connection
	assert.Equal(t, "pg_catalog", extensionSchema(conn, "plpgsql"))
	assert.Equal(t, "", extensionSchema(conn, "unknown"))

	// test with already closed connection
	conn.Close()
	assert.Equal(t, "", extensionSchema(conn, "plpgsql"))
}

func Test_isSchemaExists(t *testing.T) {
	conn, err := postgres.NewTestConnect()
	assert.NoError(t, err)

	// test with proper connection
	assert.True(t, isSchemaExists(conn, "public"))
	assert.False(t, isSchemaExists(conn, "unknown"))

	// test with already closed connection
	conn.Close()
	assert.False(t, isSchemaExists(conn, "public"))
}
