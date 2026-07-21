package app

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/gsoultan/pontus/pkg/config"
	"github.com/gsoultan/pontus/pkg/system"
)

// Options handles the command-line flags and configuration path.
type Options struct {
	ConfigPath    string
	ServiceAction string
	ShowVersion   bool
}

// NewOptions parses command-line flags and returns an options struct.
func NewOptions() *Options {
	opts := new(Options)
	flag.StringVar(&opts.ConfigPath, "config", "config.yaml", "path to config file")
	flag.StringVar(&opts.ServiceAction, "service", "", "service action (install, uninstall, start, stop, restart)")
	flag.BoolVar(&opts.ShowVersion, "version", false, "show version information")
	flag.Parse()

	// If default config doesn't exist in current dir, check standard OS location
	if opts.ConfigPath == "config.yaml" {
		if _, err := os.Stat("config.yaml"); os.IsNotExist(err) {
			standardConfig := filepath.Join(system.GetDefaultDataDir(), "config.yaml")
			if _, err := os.Stat(standardConfig); err == nil {
				opts.ConfigPath = standardConfig
			}
		}
	}

	// Handle positional arguments for backward compatibility
	if flag.NArg() > 0 && opts.ConfigPath == "config.yaml" {
		arg := flag.Arg(0)
		if arg == "ui" {
			fmt.Println("Pontus UI is embedded and runs alongside the proxy.")
			fmt.Println("To start Pontus with the UI, just run it normally.")
			fmt.Println("The dashboard will be available at the management address (default :9090).")
			os.Exit(0)
		}
		opts.ConfigPath = arg
	}
	return opts
}

// loadConfig loads the configuration from the specified path or returns defaults.
func LoadConfig(path string) *config.Options {
	cfg, err := config.Load(path)
	if err != nil {
		log.Printf("Warning: Failed to load config %s, using defaults: %v", path, err)
		return &config.Options{
			ProxyAddr: ":5432",
			MgmtAddr:  ":9090",
			Backends: []config.Backend{
				{Addr: "127.0.0.1:5433", Role: "primary"},
			},
			Protocol:       "postgres",
			DialTimeout:    5 * time.Second,
			MaxConns:       100,
			HealthInterval: 10 * time.Second,
		}
	}
	return cfg
}
