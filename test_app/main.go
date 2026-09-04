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
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

type config struct {
	Enabled      bool          `json:"enabled"`
	Message      string        `json:"message"`
	Count        int           `json:"count"`
	Port         int           `json:"port"`
	FilePath     string        `json:"file_path"`
	StopAfter    time.Duration `json:"stop_after"`
	StopDelay    time.Duration `json:"stop_delay"`
	EventPath    string        `json:"event_path"`
	SpawnChild   bool          `json:"spawn_child"`
	ChildPIDPath string        `json:"child_pid_path"`
}

var errStopAfter = errors.New("configured stop-after elapsed")

const childProcessArgument = "--daemon-test-child"

type lifecycleEvent struct {
	Event string    `json:"event"`
	PID   int       `json:"pid"`
	Time  time.Time `json:"time"`
}

type status struct {
	Config      config    `json:"config"`
	Args        []string  `json:"args"`
	Executable  string    `json:"executable"`
	FileContent string    `json:"file_content"`
	PID         int       `json:"pid"`
	ChildPID    int       `json:"child_pid,omitempty"`
	StartedAt   time.Time `json:"started_at"`
	CurrentTime time.Time `json:"current_time"`
}

type application struct {
	config      config
	args        []string
	executable  string
	fileContent string
	startedAt   time.Time
	childPID    int
	server      *http.Server
	serveErr    chan error
	startMu     sync.Mutex
	started     bool
	stopOnce    sync.Once
}

func parseConfig(args []string) (config, error) {
	var cfg config
	flags := flag.NewFlagSet("test-app", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.BoolVar(&cfg.Enabled, "enabled", false, "enable the sample feature")
	flags.StringVar(&cfg.Message, "message", "hello from test app", "message returned by the server")
	flags.IntVar(&cfg.Count, "count", 1, "sample integer value")
	flags.IntVar(&cfg.Port, "port", 18080, "HTTP listen port")
	flags.StringVar(&cfg.FilePath, "file-path", "", "file to read during startup")
	flags.DurationVar(&cfg.StopAfter, "stop-after", 0, "stop with a failure after this duration")
	flags.DurationVar(&cfg.StopDelay, "stop_delay", 0, "delay graceful shutdown after a stop request")
	flags.StringVar(&cfg.EventPath, "event-path", "", "append lifecycle events to this file")
	flags.BoolVar(&cfg.SpawnChild, "spawn-child", false, "start a child process for cleanup testing")
	flags.StringVar(&cfg.ChildPIDPath, "child-pid-path", "child.pid", "write the test child process ID to this file")
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

func readConfiguredFile(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read configured file %q: %w", path, err)
	}
	return string(content), nil
}

func newApplication(cfg config, args []string, executable, fileContent string) *application {
	app := &application{
		config:      cfg,
		args:        append([]string(nil), args...),
		executable:  executable,
		fileContent: fileContent,
		startedAt:   time.Now(),
		serveErr:    make(chan error, 1),
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
		FileContent: app.fileContent,
		PID:         os.Getpid(),
		ChildPID:    app.childPID,
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
	if err := app.recordEvent("started"); err != nil {
		_ = listener.Close()
		return err
	}
	if app.config.SpawnChild {
		if err := app.startChild(); err != nil {
			_ = listener.Close()
			return err
		}
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

func (app *application) recordEvent(event string) error {
	if app.config.EventPath == "" {
		return nil
	}
	file, err := os.OpenFile(app.config.EventPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return fmt.Errorf("open lifecycle event file: %w", err)
	}
	defer file.Close()
	if err := json.NewEncoder(file).Encode(lifecycleEvent{Event: event, PID: os.Getpid(), Time: time.Now()}); err != nil {
		return fmt.Errorf("write lifecycle event: %w", err)
	}
	return nil
}

func (app *application) startChild() error {
	command := exec.Command(app.executable, childProcessArgument)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		return fmt.Errorf("start child process: %w", err)
	}
	app.childPID = command.Process.Pid
	if err := os.WriteFile(app.config.ChildPIDPath, []byte(fmt.Sprintf("%d\n", app.childPID)), 0600); err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return fmt.Errorf("write child process ID: %w", err)
	}
	go func() {
		if err := command.Wait(); err != nil {
			log.Printf("child process exited: %v", err)
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
		if err := app.recordEvent("signal"); err != nil {
			log.Printf("record stop signal: %v", err)
		}
		app.Stop()
		if err := app.recordEvent("stopped"); err != nil {
			log.Printf("record graceful stop: %v", err)
		}
		return nil
	case <-stopTimer:
		if err := app.recordEvent("failure"); err != nil {
			log.Printf("record configured failure: %v", err)
		}
		app.shutdown()
		return fmt.Errorf("%w: %s", errStopAfter, app.config.StopAfter)
	case err := <-app.serveErr:
		return fmt.Errorf("HTTP server failed: %w", err)
	}
}

func main() {
	args := os.Args[1:]
	if len(args) == 1 && args[0] == childProcessArgument {
		interrupt := make(chan os.Signal, 1)
		signal.Notify(interrupt, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(interrupt)
		<-interrupt
		return
	}
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
	fileContent, err := readConfiguredFile(cfg.FilePath)
	if err != nil {
		log.Fatal(err)
	}

	if err := newApplication(cfg, args, executable, fileContent).run(); err != nil {
		log.Fatal(err)
	}
}
