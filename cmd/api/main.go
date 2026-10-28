package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"attendance-api/internal/auth"
	"attendance-api/internal/catalogue"
	cataloguehttp "attendance-api/internal/catalogue/httpapi"
	cataloguepostgres "attendance-api/internal/catalogue/postgres"
	"attendance-api/internal/checkins"
	checkinhttp "attendance-api/internal/checkins/httpapi"
	checkinpostgres "attendance-api/internal/checkins/postgres"
	"attendance-api/internal/clock"
	"attendance-api/internal/config"
	"attendance-api/internal/db"
	"attendance-api/internal/db/dbgen"
	"attendance-api/internal/devices"
	"attendance-api/internal/httpx"
	studentimports "attendance-api/internal/imports"
	importshttp "attendance-api/internal/imports/httpapi"
	importspostgres "attendance-api/internal/imports/postgres"
	"attendance-api/internal/notify"
	notifypostgres "attendance-api/internal/notify/postgres"
	"attendance-api/internal/registration"
	registrationhttp "attendance-api/internal/registration/httpapi"
	registrationpostgres "attendance-api/internal/registration/postgres"
	"attendance-api/internal/reports"
	reportshttp "attendance-api/internal/reports/httpapi"
	reportspostgres "attendance-api/internal/reports/postgres"
	"attendance-api/internal/scans"
	scanhttp "attendance-api/internal/scans/httpapi"
	scanpostgres "attendance-api/internal/scans/postgres"
	"attendance-api/internal/sessions"
	sessionhttp "attendance-api/internal/sessions/httpapi"
	sessionpostgres "attendance-api/internal/sessions/postgres"
	"attendance-api/internal/studentportal"
	studentportalhttp "attendance-api/internal/studentportal/httpapi"
	studentportalpostgres "attendance-api/internal/studentportal/postgres"

	"github.com/go-chi/chi/v5"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("load configuration", "error", err)
		os.Exit(1)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("connect to database", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	queries := dbgen.New(pool)
	systemClock := clock.SystemClock{}
	refreshStore, err := auth.NewPostgresStore(pool, queries)
	if err != nil {
		logger.Error("configure authentication store", "error", err)
		os.Exit(1)
	}
	tokens, err := auth.NewTokenManager(cfg.JWTSecret, systemClock, refreshStore)
	if err != nil {
		logger.Error("configure token service", "error", err)
		os.Exit(1)
	}
	loginService, err := auth.NewLoginService(refreshStore, tokens)
	if err != nil {
		logger.Error("configure login service", "error", err)
		os.Exit(1)
	}
	authHandler, err := auth.NewHandler(loginService, cfg.CookieSecure, cfg.CookieSameSite)
	if err != nil {
		logger.Error("configure authentication handler", "error", err)
		os.Exit(1)
	}
	deliveryMailer, err := notify.NewMailer(cfg.MailAPIKey, cfg.MailFrom, cfg.AppEnv, logger)
	if err != nil {
		logger.Error("configure mailer", "error", err)
		os.Exit(1)
	}
	outboxStore, err := notifypostgres.NewStore(pool)
	if err != nil {
		logger.Error("configure email outbox store", "error", err)
		os.Exit(1)
	}
	mailer, err := notify.NewQueuedMailer(outboxStore)
	if err != nil {
		logger.Error("configure queued mailer", "error", err)
		os.Exit(1)
	}
	outboxWorker, err := notify.NewWorker(outboxStore, deliveryMailer, systemClock.Now, logger)
	if err != nil {
		logger.Error("configure email outbox worker", "error", err)
		os.Exit(1)
	}
	go func() {
		if err := outboxWorker.Run(ctx, 10*time.Second); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("email outbox worker stopped", "error", err)
		}
	}()
	claimService, err := auth.NewClaimService(refreshStore, mailer, systemClock, cfg.CodeHMACSecret)
	if err != nil {
		logger.Error("configure student claim service", "error", err)
		os.Exit(1)
	}
	claimHandler, err := auth.NewClaimHandler(claimService, tokens, cfg.CookieSecure, cfg.CookieSameSite)
	if err != nil {
		logger.Error("configure student claim handler", "error", err)
		os.Exit(1)
	}
	invitationService, err := auth.NewInvitationService(refreshStore, mailer, systemClock)
	if err != nil {
		logger.Error("configure invitation service", "error", err)
		os.Exit(1)
	}
	invitationHandler, err := auth.NewInvitationHandler(invitationService, tokens, cfg.CookieSecure, cfg.CookieSameSite)
	if err != nil {
		logger.Error("configure invitation handler", "error", err)
		os.Exit(1)
	}
	if err := invitationService.SetBaseURL(cfg.AllowedOrigins[0]); err != nil {
		logger.Error("configure invitation URL", "error", err)
		os.Exit(1)
	}
	passwordResetService, err := auth.NewPasswordResetService(refreshStore, systemClock, cfg.CodeHMACSecret)
	if err != nil {
		logger.Error("configure password reset service", "error", err)
		os.Exit(1)
	}
	passwordResetHandler, err := auth.NewPasswordResetHandler(passwordResetService)
	if err != nil {
		logger.Error("configure password reset handler", "error", err)
		os.Exit(1)
	}
	signupRequestService, err := auth.NewSignupRequestService(refreshStore)
	if err != nil {
		logger.Error("configure signup request service", "error", err)
		os.Exit(1)
	}
	signupRequestHandler, err := auth.NewSignupRequestHandler(signupRequestService)
	if err != nil {
		logger.Error("configure signup request handler", "error", err)
		os.Exit(1)
	}
	roleAssignmentService, err := auth.NewRoleAssignmentService(refreshStore)
	if err != nil {
		logger.Error("configure role assignment service", "error", err)
		os.Exit(1)
	}
	roleAssignmentHandler, err := auth.NewRoleAssignmentHandler(roleAssignmentService)
	if err != nil {
		logger.Error("configure role assignment handler", "error", err)
		os.Exit(1)
	}
	directoryService, err := auth.NewDirectoryService(refreshStore)
	if err != nil {
		logger.Error("configure directory service", "error", err)
		os.Exit(1)
	}
	directoryHandler, err := auth.NewDirectoryHandler(directoryService)
	if err != nil {
		logger.Error("configure directory handler", "error", err)
		os.Exit(1)
	}
	checkinStore, err := checkinpostgres.NewPostgresStore(queries)
	if err != nil {
		logger.Error("configure check-in store", "error", err)
		os.Exit(1)
	}
	checkinService, err := checkins.NewService(checkinStore, systemClock, cfg.CodeHMACSecret, cfg.CheckinCodeWindow)
	if err != nil {
		logger.Error("configure check-in service", "error", err)
		os.Exit(1)
	}
	checkinHandler, err := checkinhttp.NewHandler(checkinService)
	if err != nil {
		logger.Error("configure check-in handler", "error", err)
		os.Exit(1)
	}
	sessionStore, err := sessionpostgres.NewPostgresStore(pool, queries)
	if err != nil {
		logger.Error("configure session store", "error", err)
		os.Exit(1)
	}
	// The check-in verifier and the code issuer must agree on the window or
	// codes would verify inconsistently across the two services.
	sessionService, err := sessions.NewService(sessionStore, systemClock, cfg.CodeHMACSecret,
		cfg.CheckinCodeWindow, 5*time.Minute)
	if err != nil {
		logger.Error("configure session service", "error", err)
		os.Exit(1)
	}
	sessionHandler, err := sessionhttp.NewHandler(sessionService, sessionStore)
	if err != nil {
		logger.Error("configure session handler", "error", err)
		os.Exit(1)
	}
	scanStore, err := scanpostgres.NewStore(pool, queries)
	if err != nil {
		logger.Error("configure device scan store", "error", err)
		os.Exit(1)
	}
	scanService, err := scans.NewService(scanStore)
	if err != nil {
		logger.Error("configure device scan service", "error", err)
		os.Exit(1)
	}
	scanHandler, err := scanhttp.NewHandler(scanService, scanStore, systemClock)
	if err != nil {
		logger.Error("configure device scan handler", "error", err)
		os.Exit(1)
	}
	deviceStore, err := devices.NewPostgresStore(pool)
	if err != nil {
		logger.Error("configure device management store", "error", err)
		os.Exit(1)
	}
	deviceService, err := devices.NewService(deviceStore, systemClock,
		time.Duration(cfg.DeviceOfflineSeconds)*time.Second)
	if err != nil {
		logger.Error("configure device management service", "error", err)
		os.Exit(1)
	}
	deviceHandler, err := devices.NewHandler(deviceService)
	if err != nil {
		logger.Error("configure device management handler", "error", err)
		os.Exit(1)
	}
	reportStore, err := reportspostgres.NewStore(pool, queries)
	if err != nil {
		logger.Error("configure reports store", "error", err)
		os.Exit(1)
	}
	reportService, err := reports.NewService(reportStore, systemClock,
		time.Duration(cfg.DeviceOfflineSeconds)*time.Second)
	if err != nil {
		logger.Error("configure reports service", "error", err)
		os.Exit(1)
	}
	reportHandler, err := reportshttp.NewHandler(reportService)
	if err != nil {
		logger.Error("configure reports handler", "error", err)
		os.Exit(1)
	}
	catalogueStore, err := cataloguepostgres.NewStore(pool, queries)
	if err != nil {
		logger.Error("configure catalogue store", "error", err)
		os.Exit(1)
	}
	catalogueService, err := catalogue.NewService(catalogueStore)
	if err != nil {
		logger.Error("configure catalogue service", "error", err)
		os.Exit(1)
	}
	catalogueHandler, err := cataloguehttp.NewHandler(catalogueService)
	if err != nil {
		logger.Error("configure catalogue handler", "error", err)
		os.Exit(1)
	}
	registrationStore, err := registrationpostgres.NewStore(queries, pool)
	if err != nil {
		logger.Error("configure registration store", "error", err)
		os.Exit(1)
	}
	registrationService, err := registration.NewService(registrationStore, systemClock)
	if err != nil {
		logger.Error("configure registration service", "error", err)
		os.Exit(1)
	}
	registrationHandler, err := registrationhttp.NewHandler(registrationService)
	if err != nil {
		logger.Error("configure registration handler", "error", err)
		os.Exit(1)
	}
	studentPortalStore, err := studentportalpostgres.NewStore(pool, queries,
		time.Duration(cfg.DeviceOfflineSeconds)*time.Second)
	if err != nil {
		logger.Error("configure student portal store", "error", err)
		os.Exit(1)
	}
	studentPortalService, err := studentportal.NewService(studentPortalStore, systemClock)
	if err != nil {
		logger.Error("configure student portal service", "error", err)
		os.Exit(1)
	}
	studentPortalHandler, err := studentportalhttp.NewHandler(studentPortalService)
	if err != nil {
		logger.Error("configure student portal handler", "error", err)
		os.Exit(1)
	}
	importStore, err := importspostgres.NewStore(pool)
	if err != nil {
		logger.Error("configure student import store", "error", err)
		os.Exit(1)
	}
	if err := importStore.SetInvitationBaseURL(cfg.AllowedOrigins[0]); err != nil {
		logger.Error("configure staff invitation URL", "error", err)
		os.Exit(1)
	}
	importService, err := studentimports.NewService(importStore, studentimports.MaxStudentCSVBytes)
	if err != nil {
		logger.Error("configure student import service", "error", err)
		os.Exit(1)
	}
	importHandler, err := importshttp.NewHandler(importService)
	if err != nil {
		logger.Error("configure student import handler", "error", err)
		os.Exit(1)
	}
	handler := httpx.NewRouter(pool, cfg.AllowedOrigins, func(router chi.Router) {
		authHandler.RegisterRoutes(router)
		claimHandler.RegisterRoutes(router)
		catalogueHandler.RegisterPublicRoutes(router)
		invitationHandler.RegisterPublicRoutes(router)
		passwordResetHandler.RegisterRoutes(router)
		sessionHandler.RegisterRoutes(router.With(auth.UserAuth(tokens)))
		router.With(auth.UserAuth(tokens)).Group(func(userRouter chi.Router) {
			invitationHandler.RegisterAuthenticatedRoutes(userRouter)
			signupRequestHandler.RegisterRoutes(userRouter)
			roleAssignmentHandler.RegisterRoutes(userRouter)
			directoryHandler.RegisterRoutes(userRouter)
			catalogueHandler.RegisterRoutes(userRouter)
			registrationHandler.RegisterRoutes(userRouter)
			studentPortalHandler.RegisterRoutes(userRouter)
			importHandler.RegisterRoutes(userRouter)
			reportHandler.RegisterRoutes(userRouter)
			deviceHandler.RegisterAdminRoutes(userRouter)
		})
		router.With(httpx.RateLimitByIP(20, time.Minute)).Group(func(pairingRouter chi.Router) {
			deviceHandler.RegisterPairingRoutes(pairingRouter)
		})
		router.With(auth.UserAuth(tokens), httpx.RateLimitByIP(20, time.Minute),
			auth.UserRateLimit(10, time.Minute)).Post("/checkins", checkinHandler.ServeHTTP)
		router.With(httpx.RateLimitByIP(60, time.Minute), scanhttp.DeviceAuth(scanStore, systemClock),
			scanhttp.DeviceRateLimit(30, time.Minute)).Group(func(deviceRouter chi.Router) {
			scanHandler.RegisterRoutes(deviceRouter)
		})
	})
	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Error("shutdown HTTP server", "error", err)
		}
	}()

	logger.Info("starting HTTP server", "addr", cfg.HTTPAddr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("serve HTTP", "error", err)
		os.Exit(1)
	}
}
