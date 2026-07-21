package main

import (
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"

	"github.com/gsoultan/pontus/internal/app"
	"github.com/gsoultan/pontus/pkg/observability"
	"github.com/gsoultan/pontus/pkg/version"
	"github.com/gsoultan/pontus/server/management/store"
)

//go:generate buf generate
//go:generate bun --cwd ../../web install
//go:generate bun --cwd ../../web run build

func main() {
	// Initialize Global Logger with Broadcasting
	slog.SetDefault(slog.New(observability.NewBroadcastHandler(
		slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}),
		observability.GlobalLogBroadcaster,
	)))

	// Parse flags and load configuration
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [options] [command]\n\n", os.Args[0])
		fmt.Fprintln(os.Stderr, "Options:")
		flag.PrintDefaults()
		fmt.Fprintln(os.Stderr, "\nCommands:")
		fmt.Fprintln(os.Stderr, "  create-user    Create a new management user (bootstrap via local DB)")
	}
	opts := app.NewOptions()

	if opts.ShowVersion {
		fmt.Print(version.Info())
		return
	}

	// Handle custom subcommands
	args := flag.Args()
	if len(args) > 0 {
		switch args[0] {
		case "create-user":
			handleCreateUser(args[1:])
			return
		}
	}

	cfg := app.LoadConfig(opts.ConfigPath)

	// Create application
	var runner app.Runner = app.NewApp(cfg)

	// Handle service management or run as service
	svc := app.NewPontusService(runner, opts.ConfigPath)
	if opts.ServiceAction != "" {
		if err := svc.HandleAction(opts.ServiceAction); err != nil {
			log.Fatalf("Service action failed: %v", err)
		}
		return
	}

	if err := svc.HandleAction(""); err != nil {
		log.Fatalf("Application error: %v", err)
	}
}

func handleCreateUser(args []string) {
	fs := flag.NewFlagSet("create-user", flag.ExitOnError)
	username := fs.String("username", "", "Username")
	password := fs.String("password", "", "Password")
	role := fs.String("role", "admin", "Role (admin, viewer)")
	dbPath := fs.String("db", "management.db", "Path to management.db")

	if err := fs.Parse(args); err != nil {
		log.Fatalf("Failed to parse flags: %v", err)
	}

	if *username == "" || *password == "" {
		fmt.Println("Usage: pontus create-user -username <user> -password <pass> [-role <role>] [-db <path>]")
		os.Exit(1)
	}

	db, err := store.NewManagementDB(*dbPath)
	if err != nil {
		log.Fatalf("Failed to open management database: %v", err)
	}
	defer db.Close()

	userStore := store.NewSQLiteUser(db)

	if err := userStore.Upsert(*username, *password, *role); err != nil {
		log.Fatalf("Failed to create user: %v", err)
	}

	fmt.Printf("User %s (%s) created successfully in %s\n", *username, *role, *dbPath)
}
