package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/z46-dev/gasket"
	"github.com/z46-dev/golog"
	"github.com/z46-dev/llama-stack/internal/config"
	"github.com/z46-dev/llama-stack/internal/resource"
	"github.com/z46-dev/llama-stack/internal/resourceapi"
)

var version string = "development"
var logger *golog.Logger = golog.New().Timestamp().Precision(golog.PrecisionSecond).Representation(false, false)

const resourceExpiryTask string = "resource-expiry"

type health struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

// main runs the initial orchestration health endpoint.
func main() {
	var err error

	if err = run(); err != nil {
		logger.Error(fmt.Sprintf("llama-stackd stopped: %v", err))
		os.Exit(1)
	}
}

// run serves health checks and performs a bounded graceful shutdown.
func run() (err error) {
	var (
		cfg             config.Config
		server          *http.Server
		manager         *resource.Manager
		jobs            *gasket.Client
		resourceHandler http.Handler
		adminToken      []byte
		shutdownContext context.Context
		cancel          context.CancelFunc
		signalContext   context.Context
		stop            context.CancelFunc
		serverErrors    chan error = make(chan error, 1)
		jobErrors       chan error = make(chan error, 1)
		scheduleExpiry  resourceapi.ExpiryScheduler
		leases          []resource.Lease
	)

	if cfg, err = config.Load(config.Path()); err != nil {
		return
	}
	if cfg.Ports.Enabled {
		if adminToken, err = os.ReadFile(cfg.Ports.AdminTokenFile); err != nil {
			return fmt.Errorf("read resource administrator token: %w", err)
		}
		if manager, err = resource.Open(resource.Config{
			DatabasePath: cfg.Ports.Database,
			ListenHost:   cfg.Ports.ListenHost,
			PortStart:    cfg.Ports.Start,
			PortEnd:      cfg.Ports.End,
			DefaultTTL:   time.Duration(cfg.Ports.DefaultTTL) * time.Second,
			MaximumTTL:   time.Duration(cfg.Ports.MaximumTTL) * time.Second,
			MaximumRun:   cfg.Ports.MaximumPerRun,
			MaximumUser:  cfg.Ports.MaximumPerUser,
		}); err != nil {
			return
		}
		defer manager.Close()
		if jobs, err = gasket.NewClient(
			cfg.Jobs.Database,
			gasket.PollInterval(time.Duration(cfg.Jobs.PollInterval)*time.Millisecond),
			gasket.DatabaseLockRetry(cfg.Jobs.DatabaseLockRetries, time.Duration(cfg.Jobs.DatabaseLockDelay)*time.Millisecond),
			gasket.TaskRecoveryTimeout(time.Duration(cfg.Jobs.RecoveryTimeout)*time.Second),
		); err != nil {
			return fmt.Errorf("open job scheduler: %w", err)
		}
		defer jobs.Close()
		if err = jobs.RegisterConsumer(resourceExpiryTask, func(_ int, _ []byte) (result gasket.TaskConsumerResult) {
			result.Error = manager.Reap()
			result.Success = result.Error == nil
			return
		}); err != nil {
			return fmt.Errorf("register resource expiration consumer: %w", err)
		}
		scheduleExpiry = func(expiresAt time.Time) (scheduleErr error) {
			var delay time.Duration = time.Until(expiresAt)
			if delay < 0 {
				delay = 0
			}
			_, scheduleErr = jobs.NewTask(resourceExpiryTask, nil, gasket.RunIn(delay), gasket.RetryPolicy(3, time.Second))
			return
		}
		if leases, err = manager.ListAll(); err != nil {
			return fmt.Errorf("list leases for expiration scheduling: %w", err)
		}
		for _, lease := range leases {
			if err = scheduleExpiry(lease.ExpiresAt); err != nil {
				return fmt.Errorf("schedule restored lease expiration: %w", err)
			}
		}
		resourceHandler = resourceapi.NewHandler(manager, strings.TrimSpace(string(adminToken)), time.Duration(cfg.Ports.MaximumWait)*time.Second, scheduleExpiry)
	}

	server = &http.Server{
		Addr:              cfg.Orchestrator.Host + ":" + strconv.Itoa(cfg.Orchestrator.Port),
		Handler:           routes(resourceHandler),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      time.Duration(cfg.Ports.MaximumWait)*time.Second + 15*time.Second,
		IdleTimeout:       60 * time.Second,
	}

	signalContext, stop = signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		serverErrors <- server.ListenAndServe()
	}()
	if jobs != nil {
		go func() {
			jobErrors <- jobs.Run(signalContext)
		}()
	}

	select {
	case <-signalContext.Done():
		shutdownContext, cancel = context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err = server.Shutdown(shutdownContext)
	case err = <-serverErrors:
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
	case err = <-jobErrors:
		if signalContext.Err() != nil {
			shutdownContext, cancel = context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			err = server.Shutdown(shutdownContext)
		} else if err == nil {
			err = errors.New("job scheduler stopped unexpectedly")
		}
	}

	return
}

// routes builds the deliberately small first-pass HTTP surface.
func routes(resourceHandler http.Handler) (handler http.Handler) {
	var mux *http.ServeMux = http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(writer http.ResponseWriter, _ *http.Request) {
		var encodeErr error

		writer.Header().Set("Content-Type", "application/json")
		if encodeErr = json.NewEncoder(writer).Encode(health{Status: "ok", Version: version}); encodeErr != nil {
			logger.Warning(fmt.Sprintf("write health response: %v", encodeErr))
		}
	})
	if resourceHandler != nil {
		mux.Handle("/v1/", resourceHandler)
	}

	handler = mux
	return
}
