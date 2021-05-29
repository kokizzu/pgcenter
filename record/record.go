// 'pgcenter record' - collects Postgres statistics and record to persistent store.

package record

import (
	"fmt"
	"github.com/lesovsky/pgcenter/internal/postgres"
	"github.com/lesovsky/pgcenter/internal/query"
	"github.com/lesovsky/pgcenter/internal/stat"
	"github.com/lesovsky/pgcenter/internal/view"
	"os"
	"os/signal"
	"strings"
	"time"
)

// Config defines config container for configuring 'pgcenter record'.
type Config struct {
	Interval    time.Duration // Statistics recording interval
	Count       int           // Number of statistics snapshot to record
	OutputFile  string        // File where statistics will be saved
	AppendFile  bool          // Append data to file
	StringLimit int           // Limit of the length, to which query should be trimmed
}

// RunMain is the 'pgcenter record' main entry point.
func RunMain(dbConfig postgres.Config, config Config) error {
	app := newApp(config, dbConfig)

	err := app.setup()
	if err != nil {
		return err
	}

	fmt.Printf("INFO: recording to %s\n", config.OutputFile)

	// In case of SIGINT stop program gracefully
	doQuit := make(chan os.Signal, 1)
	signal.Notify(doQuit, os.Interrupt)

	// Run recording loop
	return app.record(doQuit)
}

// app defines 'pgcenter record' runtime dependencies.
type app struct {
	config   Config
	dbConfig postgres.Config
	views    view.Views
	recorder recorder
}

// newApp creates new 'pgcenter record' app.
func newApp(config Config, dbConfig postgres.Config) *app {
	return &app{
		config:   config,
		dbConfig: dbConfig,
	}
}

// setup configures necessary queries depending on Postgres version.
func (app *app) setup() error {
	db, err := postgres.Connect(app.dbConfig)
	if err != nil {
		return err
	}
	defer db.Close()

	props, err := stat.GetPostgresProperties(db)
	if err != nil {
		return err
	}

	// Create and configure stats views depending on running Postgres.
	opts := query.NewOptions(props.VersionNum, props.Recovery, props.GucTrackCommitTimestamp, app.config.StringLimit, props.ExtPGSSSchema)

	n, views := filterViews(props.VersionNum, props.ExtPGSSSchema, view.New())
	if n > 0 {
		fmt.Println("INFO: some statistics is not supported by the current version of Postgres and will be skipped")
	}

	err = views.Configure(opts)
	if err != nil {
		return err
	}

	app.views = views

	// Create tar recorder.
	app.recorder = newTarRecorder(tarConfig{
		filename: app.config.OutputFile,
		append:   app.config.AppendFile,
	})

	return nil
}

// record collects statistics and stores into file.
func (app *app) record(doQuit chan os.Signal) error {
	var (
		count    = app.config.Count
		interval = app.config.Interval
	)

	t := time.NewTicker(interval)

	// record the number of snapshots requested by user (or record continuously until SIGINT will be received)
	var n int
	for {
		if count > 0 && n >= count {
			break
		} else {
			n++
		}

		err := app.recorder.open()
		if err != nil {
			return err
		}

		stats, err := app.recorder.collect(app.dbConfig, app.views)
		if err != nil {
			return err
		}

		err = app.recorder.write(stats)
		if err != nil {
			return err
		}

		err = app.recorder.close()
		if err != nil {
			return err
		}

		select {
		case <-t.C:
			continue
		case sig := <-doQuit:
			t.Stop()
			return fmt.Errorf("got %s", sig.String())
		}
	}

	return nil
}

// filterViews removes views which are not suitable for specified version and used configuration.
func filterViews(version int, pgssSchema string, views view.Views) (int, view.Views) {
	var filtered int
	var pgssNotfound bool

	for k, v := range views {
		if !v.VersionOK(version) {
			delete(views, k)
			filtered++
			continue
		}

		// Skip statements views if schema, where pg_stat_statements is installed, not found.
		if strings.HasPrefix(k, "statements_") && pgssSchema == "" {
			delete(views, k)
			filtered++
			pgssNotfound = true
		}
	}

	if pgssNotfound {
		fmt.Println("INFO: pg_stat_statements not found, skip recording it")
	}

	return filtered, views
}
