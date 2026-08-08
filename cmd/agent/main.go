package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net"
	"os"

	"github.com/gsoultan/pontus/agent/endpoint"
	"github.com/gsoultan/pontus/agent/infrastructure"
	"github.com/gsoultan/pontus/agent/transport"
	"github.com/gsoultan/pontus/api/proto/service"
	pkgservice "github.com/gsoultan/pontus/pkg/service"
	"github.com/gsoultan/pontus/pkg/version"
	"google.golang.org/grpc"
	_ "google.golang.org/grpc/encoding/gzip"
	"google.golang.org/grpc/reflection"
)

func main() {
	addr := flag.String("addr", ":9091", "Agent gRPC address")
	token := flag.String("token", "", "Agent authentication token (or PONTUS_AGENT_TOKEN)")
	insecure := flag.Bool("insecure", false,
		"Serve without authentication. Every RPC becomes reachable by anyone who can reach the port, "+
			"including InstallDatabase, PromoteNode and RemoveDatabase. Localhost-bound testing only.")
	svcCmd := flag.String("service", "", "Service command: install, uninstall, start, stop, status")
	showVersion := flag.Bool("version", false, "Show version information")
	flag.Parse()

	if *showVersion {
		fmt.Print(version.Info())
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// The token may come from the environment so it does not sit in a process
	// listing or a service definition.
	if *token == "" {
		*token = os.Getenv("PONTUS_AGENT_TOKEN")
	}

	// Refuse before the service machinery starts, so this exits with a message
	// and a non-zero status instead of becoming a hung process.
	if *token == "" && !*insecure {
		log.Fatal("agent token is required: pass -token or set PONTUS_AGENT_TOKEN " +
			"(use -insecure only for localhost-bound testing)")
	}

	// -token is deliberately not propagated into the installed service: the
	// service definition is a file on disk and the argv is visible in a process
	// listing. The installed unit reads PONTUS_AGENT_TOKEN instead.
	args := []string{"-addr", *addr}
	if *insecure {
		args = append(args, "-insecure")
	}

	cfg := pkgservice.Config{
		Name:        "pontus-agent",
		DisplayName: "Pontus Agent",
		Description: "Pontus Monitoring Agent",
		Arguments:   args,
	}

	mgr, err := pkgservice.NewManager(cfg, func() error {
		return runAgent(ctx, *addr, *token, *insecure)
	}, func() error {
		cancel()
		return nil
	})

	if err != nil {
		log.Fatalf("Failed to create service manager: %v", err)
	}

	if *svcCmd != "" {
		if *svcCmd == "install" && !*insecure {
			log.Print("The installed service reads its token from PONTUS_AGENT_TOKEN. " +
				"Set it in the service environment or the agent will refuse to start.")
		}
		if err := handleServiceCommand(mgr, *svcCmd); err != nil {
			log.Fatal(err)
		}
		return
	}

	if err := mgr.Run(); err != nil {
		log.Fatal(err)
	}
}

func handleServiceCommand(mgr pkgservice.Manager, cmd string) error {
	var err error
	switch cmd {
	case "install":
		err = mgr.Install()
		if err == nil {
			log.Println("Service installed successfully")
		}
	case "uninstall":
		err = mgr.Uninstall()
		if err == nil {
			log.Println("Service uninstalled successfully")
		}
	case "start":
		err = mgr.Start()
		if err == nil {
			log.Println("Service started successfully")
		}
	case "stop":
		err = mgr.Stop()
		if err == nil {
			log.Println("Service stopped successfully")
		}
	case "status":
		var status string
		status, err = mgr.Status()
		if err == nil {
			log.Printf("Service status: %s", status)
		}
	default:
		return fmt.Errorf("unknown service command: %s", cmd)
	}
	return err
}

func runAgent(ctx context.Context, addr string, token string, insecure bool) error {
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}

	// Fail closed.
	//
	// The interceptor used to be attached only when a token happened to be set,
	// so an agent started without one served every RPC unauthenticated — and
	// this agent installs database software, promotes nodes and removes
	// databases, as root. An orchestrator with no front door is the most
	// dangerous shape this program can take, so refusing to start is the only
	// safe default.
	var opts []grpc.ServerOption
	switch {
	case token != "":
		opts = append(opts,
			grpc.UnaryInterceptor(transport.TokenInterceptor(token)),
			grpc.StreamInterceptor(transport.StreamTokenInterceptor(token)))
	case insecure:
		slog.Warn("Agent is serving WITHOUT authentication",
			"addr", addr,
			"exposed", "InstallDatabase, PromoteNode, RemoveDatabase, ExecuteCommand",
			"hint", "pass -token or PONTUS_AGENT_TOKEN")
	default:
		return fmt.Errorf("agent token is required: pass -token or set PONTUS_AGENT_TOKEN " +
			"(use -insecure only for localhost-bound testing)")
	}

	s := grpc.NewServer(opts...)
	svc := infrastructure.NewService()
	if err := svc.Start(ctx); err != nil {
		return fmt.Errorf("failed to start service: %w", err)
	}
	endpoints := endpoint.MakeEndpoints(svc)
	handler := transport.NewGRPCServer(endpoints, svc)
	service.RegisterAgentServiceServer(s, handler)

	// Register reflection service on gRPC server.
	reflection.Register(s)

	log.Printf("Pontus Agent listening on %s", addr)

	errCh := make(chan error, 1)
	go func() {
		if err := s.Serve(lis); err != nil {
			errCh <- fmt.Errorf("failed to serve: %w", err)
		}
	}()

	select {
	case <-ctx.Done():
		log.Println("Shutting down Pontus Agent...")
		s.GracefulStop()
		log.Println("Pontus Agent shutdown complete")
		return nil
	case err := <-errCh:
		return err
	}
}
