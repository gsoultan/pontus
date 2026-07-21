package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"

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
	token := flag.String("token", "", "Agent authentication token")
	svcCmd := flag.String("service", "", "Service command: install, uninstall, start, stop, status")
	showVersion := flag.Bool("version", false, "Show version information")
	flag.Parse()

	if *showVersion {
		fmt.Print(version.Info())
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := pkgservice.Config{
		Name:        "pontus-agent",
		DisplayName: "Pontus Agent",
		Description: "Pontus Monitoring Agent",
		Arguments:   []string{"-addr", *addr},
	}

	mgr, err := pkgservice.NewManager(cfg, func() error {
		return runAgent(ctx, *addr, *token)
	}, func() error {
		cancel()
		return nil
	})

	if err != nil {
		log.Fatalf("Failed to create service manager: %v", err)
	}

	if *svcCmd != "" {
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

func runAgent(ctx context.Context, addr string, token string) error {
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}

	var opts []grpc.ServerOption
	if token != "" {
		opts = append(opts, grpc.UnaryInterceptor(transport.TokenInterceptor(token)), grpc.StreamInterceptor(transport.StreamTokenInterceptor(token)))
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
