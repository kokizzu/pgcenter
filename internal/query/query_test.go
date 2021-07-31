package query

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestFormat(t *testing.T) {
	opts := Options{
		WalFunction1: "pg_wal_lsn_diff",
		WalFunction2: "pg_current_wal_lsn",
	}
	got, err := Format(PgStatReplicationDefault, opts)
	assert.NoError(t, err)
	assert.Equal(
		t,
		`SELECT pid AS pid, host(client_addr) AS client, usename AS user, application_name AS name, state, sync_state AS mode, (pg_wal_lsn_diff(pg_current_wal_lsn(),'0/0') / 1024)::bigint AS "wal,KiB", (pg_wal_lsn_diff(pg_current_wal_lsn(),sent_lsn) / 1024)::bigint AS "pending,KiB", (pg_wal_lsn_diff(sent_lsn,write_lsn) / 1024)::bigint AS "write,KiB", (pg_wal_lsn_diff(write_lsn,flush_lsn) / 1024)::bigint AS "flush,KiB", (pg_wal_lsn_diff(flush_lsn,replay_lsn) / 1024)::bigint AS "replay,KiB", (pg_wal_lsn_diff(pg_current_wal_lsn(),replay_lsn))::bigint / 1024 AS "total,KiB", coalesce(date_trunc('seconds', write_lag), '0 seconds'::interval)::text AS write, coalesce(date_trunc('seconds', flush_lag), '0 seconds'::interval)::text AS flush, coalesce(date_trunc('seconds', replay_lag), '0 seconds'::interval)::text AS replay FROM pg_stat_replication ORDER BY pid DESC`,
		got,
	)

	_, err = Format("{{", opts)
	assert.Error(t, err)

	_, err = Format("{{ .Invalid }}", opts)
	assert.Error(t, err)
}

func TestNewOptions(t *testing.T) {
	testcases := []struct {
		version  int
		recovery string
		track    string
		querylen int
		want     Options
	}{
		{version: PostgresV13, recovery: "f", track: "on", querylen: 256, want: Options{
			Version: PostgresV13, Recovery: "f", GucTrackCommitTS: "on",
			ViewType: "user", WalFunction1: "pg_wal_lsn_diff", WalFunction2: "pg_current_wal_lsn",
			QueryAgeThresh: "00:00:00.0", ShowNoIdle: true, PGSSSchema: "public", PgSSQueryLen: 256, PgSSQueryLenFn: "left(p.query, 256)",
		}},
		{version: PostgresV13, recovery: "t", track: "on", querylen: 256, want: Options{
			Version: PostgresV13, Recovery: "t", GucTrackCommitTS: "on",
			ViewType: "user", WalFunction1: "pg_wal_lsn_diff", WalFunction2: "pg_last_wal_receive_lsn",
			QueryAgeThresh: "00:00:00.0", ShowNoIdle: true, PGSSSchema: "public", PgSSQueryLen: 256, PgSSQueryLenFn: "left(p.query, 256)",
		}},
		{version: PostgresV96, recovery: "f", track: "on", querylen: 256, want: Options{
			Version: PostgresV96, Recovery: "f", GucTrackCommitTS: "on",
			ViewType: "user", WalFunction1: "pg_xlog_location_diff", WalFunction2: "pg_current_xlog_location",
			QueryAgeThresh: "00:00:00.0", ShowNoIdle: true, PGSSSchema: "public", PgSSQueryLen: 256, PgSSQueryLenFn: "left(p.query, 256)",
		}},
		{version: PostgresV96, recovery: "t", track: "on", querylen: 256, want: Options{
			Version: PostgresV96, Recovery: "t", GucTrackCommitTS: "on",
			ViewType: "user", WalFunction1: "pg_xlog_location_diff", WalFunction2: "pg_last_xlog_receive_location",
			QueryAgeThresh: "00:00:00.0", ShowNoIdle: true, PGSSSchema: "public", PgSSQueryLen: 256, PgSSQueryLenFn: "left(p.query, 256)",
		}},
		{version: PostgresV13, recovery: "f", track: "on", querylen: 0, want: Options{
			Version: PostgresV13, Recovery: "f", GucTrackCommitTS: "on",
			ViewType: "user", WalFunction1: "pg_wal_lsn_diff", WalFunction2: "pg_current_wal_lsn",
			QueryAgeThresh: "00:00:00.0", ShowNoIdle: true, PGSSSchema: "public", PgSSQueryLen: 0, PgSSQueryLenFn: "p.query",
		}},
		{version: PostgresV13, recovery: "f", track: "on", querylen: 123, want: Options{
			Version: PostgresV13, Recovery: "f", GucTrackCommitTS: "on",
			ViewType: "user", WalFunction1: "pg_wal_lsn_diff", WalFunction2: "pg_current_wal_lsn",
			QueryAgeThresh: "00:00:00.0", ShowNoIdle: true, PGSSSchema: "public", PgSSQueryLen: 123, PgSSQueryLenFn: "left(p.query, 123)",
		}},
	}

	for _, tc := range testcases {
		opts := NewOptions(tc.version, tc.recovery, tc.track, tc.querylen, "public")
		assert.Equal(t, tc.want, opts)
	}
}

func Test_selectWalFunctions(t *testing.T) {
	testcases := []struct {
		version  int
		recovery string
		want1    string
		want2    string
	}{
		{version: 90500, recovery: "f", want1: "pg_xlog_location_diff", want2: "pg_current_xlog_location"},
		{version: 90500, recovery: "t", want1: "pg_xlog_location_diff", want2: "pg_last_xlog_receive_location"},
		{version: 90600, recovery: "f", want1: "pg_xlog_location_diff", want2: "pg_current_xlog_location"},
		{version: 90600, recovery: "t", want1: "pg_xlog_location_diff", want2: "pg_last_xlog_receive_location"},
		{version: 100000, recovery: "f", want1: "pg_wal_lsn_diff", want2: "pg_current_wal_lsn"},
		{version: 100000, recovery: "t", want1: "pg_wal_lsn_diff", want2: "pg_last_wal_receive_lsn"},
		{version: 110000, recovery: "f", want1: "pg_wal_lsn_diff", want2: "pg_current_wal_lsn"},
		{version: 110000, recovery: "t", want1: "pg_wal_lsn_diff", want2: "pg_last_wal_receive_lsn"},
		{version: 120000, recovery: "f", want1: "pg_wal_lsn_diff", want2: "pg_current_wal_lsn"},
		{version: 120000, recovery: "t", want1: "pg_wal_lsn_diff", want2: "pg_last_wal_receive_lsn"},
		{version: 130000, recovery: "f", want1: "pg_wal_lsn_diff", want2: "pg_current_wal_lsn"},
		{version: 130000, recovery: "t", want1: "pg_wal_lsn_diff", want2: "pg_last_wal_receive_lsn"},
	}

	for _, tc := range testcases {
		fn1, fn2 := selectWalFunctions(tc.version, tc.recovery)
		assert.Equal(t, tc.want1, fn1)
		assert.Equal(t, tc.want2, fn2)
	}
}
