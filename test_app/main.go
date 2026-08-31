package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"

	daemon "github.com/LazyEasyDev/daemon/daemon_util"
)

type config struct {
	Enabled   bool          `json:"enabled"`
	Message   string        `json:"message"`
	Count     int           `json:"count"`
	Port      int           `json:"port"`
	StopAfter time.Duration `json:"stop_after"`
	StopDelay time.Duration `json:"stop_delay"`
}

var errStopAfter = errors.New("configured stop-after elapsed")

type status struct {
	Config      config    `json:"config"`
	Args        []string  `json:"args"`
	Executable  string    `json:"executable"`
	PID         int       `json:"pid"`
	StartedAt   time.Time `json:"started_at"`
	CurrentTime time.Time `json:"current_time"`
}

type application struct {
	config     config
	args       []string
	executable string
	startedAt  time.Time
	server     *http.Server
	serveErr   chan error
	startMu    sync.Mutex
	started    bool
	stopOnce   sync.Once
}

func parseConfig(args []string) (config, error) {
	var cfg config
	flags := flag.NewFlagSet("test-app", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.BoolVar(&cfg.Enabled, "enabled", false, "enable the sample feature")
	flags.StringVar(&cfg.Message, "message", "hello from test app", "message returned by the server")
	flags.IntVar(&cfg.Count, "count", 1, "sample integer value")
	flags.IntVar(&cfg.Port, "port", 18080, "HTTP listen port")
	flags.DurationVar(&cfg.StopAfter, "stop-after", 0, "stop with a failure after this duration")
	flags.DurationVar(&cfg.StopDelay, "stop_delay", 0, "delay graceful shutdown after a stop request")
	if err := flags.Parse(args); err != nil {
		return config{}, err
	}
	if flags.NArg() != 0 {
		return config{}, fmt.Errorf("unexpected positional arguments: %v", flags.Args())
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return config{}, fmt.Errorf("port must be between 1 and 65535")
	}
	if cfg.StopAfter < 0 {
		return config{}, fmt.Errorf("stop-after must not be negative")
	}
	if cfg.StopDelay < 0 {
		return config{}, fmt.Errorf("stop_delay must not be negative")
	}
	return cfg, nil
}

func newApplication(cfg config, args []string, executable string) *application {
	app := &application{
		config:     cfg,
		args:       append([]string(nil), args...),
		executable: executable,
		startedAt:  time.Now(),
		serveErr:   make(chan error, 1),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", app.handleStatus)
	mux.HandleFunc("/healthz", app.handleHealth)
	app.server = &http.Server{
		Addr:              fmt.Sprintf("127.0.0.1:%d", cfg.Port),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return app
}

func (app *application) handleStatus(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Content-Type", "application/json")
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(status{
		Config:      app.config,
		Args:        app.args,
		Executable:  app.executable,
		PID:         os.Getpid(),
		StartedAt:   app.startedAt,
		CurrentTime: time.Now(),
	}); err != nil {
		log.Printf("write status response: %v", err)
	}
}

func (app *application) handleHealth(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(writer, "ok\n")
}

func (app *application) Start() {
	if err := app.start(); err != nil {
		log.Fatalf("start HTTP server: %v", err)
	}
}

func (app *application) start() error {
	app.startMu.Lock()
	defer app.startMu.Unlock()
	if app.started {
		return nil
	}

	listener, err := net.Listen("tcp", app.server.Addr)
	if err != nil {
		return err
	}
	app.started = true
	go func() {
		log.Printf("test app started at %s and is listening on http://127.0.0.1:%d", app.startedAt.Format(time.RFC3339Nano), app.config.Port)
		if err := app.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			app.serveErr <- err
		}
	}()
	return nil
}

func (app *application) Stop() {
	app.stopOnce.Do(func() {
		if app.config.StopDelay > 0 {
			log.Printf("delaying graceful stop for %s", app.config.StopDelay)
			time.Sleep(app.config.StopDelay)
		}
		app.shutdown()
	})
}

func (app *application) shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := app.server.Shutdown(ctx); err != nil {
		log.Printf("stop HTTP server: %v", err)
	}
}

func (app *application) Run() {
	if err := app.run(); err != nil {
		log.Fatal(err)
	}
}

func (app *application) run() error {
	if err := app.start(); err != nil {
		return fmt.Errorf("start HTTP server: %w", err)
	}

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(interrupt)

	var stopTimer <-chan time.Time
	if app.config.StopAfter > 0 {
		timer := time.NewTimer(app.config.StopAfter)
		defer timer.Stop()
		stopTimer = timer.C
	}

	select {
	case received := <-interrupt:
		log.Printf("received %s", received)
		app.Stop()
		return nil
	case <-stopTimer:
		app.shutdown()
		return fmt.Errorf("%w: %s", errStopAfter, app.config.StopAfter)
	case err := <-app.serveErr:
		return fmt.Errorf("HTTP server failed: %w", err)
	}
}

func main() {
	args := os.Args[1:]
	cfg, err := parseConfig(args)
	if err != nil {
		log.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		log.Fatal(err)
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		log.Fatal(err)
	}

	kind := daemon.SystemDaemon
	if runtime.GOOS == "darwin" {
		kind = daemon.UserAgent
	}
	service, err := daemon.NewWithExecutable(
		filepath.Base(executable),
		"Daemon test HTTP application",
		executable,
		kind,
	)
	if err != nil {
		log.Fatal(err)
	}
	message, err := service.Run(newApplication(cfg, args, executable))
	if err != nil {
		log.Fatalf("%s: %v", message, err)
	}
}
