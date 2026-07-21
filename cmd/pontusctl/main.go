package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"text/tabwriter"

	"connectrpc.com/connect"
	"github.com/gsoultan/pontus/api/proto/domain"
	"github.com/gsoultan/pontus/api/proto/endpoints"
	"github.com/gsoultan/pontus/api/proto/service/serviceconnect"
)

func main() {
	serverAddr := flag.String("addr", "http://localhost:9090", "Management API address")
	token := flag.String("token", os.Getenv("PONTUS_TOKEN"), "Authentication token")
	flag.Usage = usage
	flag.Parse()

	args := flag.Args()
	if len(args) < 1 {
		usage()
		os.Exit(1)
	}

	client := serviceconnect.NewManagementServiceClient(
		http.DefaultClient,
		*serverAddr,
	)

	ctx := context.Background()

	switch args[0] {
	case "status":
		handleStatus(ctx, client)
	case "add-backend":
		handleAddBackend(ctx, client, args[1:])
	case "remove-backend":
		handleRemoveBackend(ctx, client, args[1:])
	case "drain":
		handleDrain(ctx, client, args[1:])
	case "provision-replica":
		handleProvisionReplica(ctx, client, args[1:])
	case "logs":
		handleLogs(ctx, client, args[1:])
	case "login":
		handleLogin(ctx, client, args[1:])
	case "create-user":
		handleCreateUserAPI(ctx, client, *token, args[1:])
	default:
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Println("Usage: pontusctl [global-flags] <command> [command-flags]")
	fmt.Println("\nCommands:")
	fmt.Println("  status             Show current status of all backends")
	fmt.Println("  add-backend        Add a new backend server")
	fmt.Println("  remove-backend     Remove a backend server")
	fmt.Println("  drain              Gracefully stop using a backend")
	fmt.Println("  provision-replica  Automate setup of a new replica")
	fmt.Println("  logs               Tail real-time logs from the proxy")
	fmt.Println("  login              Authenticate and get a token")
	fmt.Println("  create-user        Create a new management user (requires admin token)")
	fmt.Println("\nGlobal Flags:")
	flag.PrintDefaults()
}

func handleStatus(ctx context.Context, client serviceconnect.ManagementServiceClient) {
	resp, err := client.GetStatus(ctx, connect.NewRequest(&endpoints.GetStatusRequest{}))
	if err != nil {
		log.Fatalf("Failed to get status: %v", err)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "ADDRESS\tROLE\tHEALTHY\tCONNS\tLATENCY")
	for _, b := range resp.Msg.Backends {
		healthy := "Healthy"
		if !b.Healthy {
			healthy = "Unhealthy"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%dms\n", b.Address, b.Role, healthy, b.ActiveConns, b.LatencyMs)
	}
	w.Flush()
}

func handleAddBackend(ctx context.Context, client serviceconnect.ManagementServiceClient, args []string) {
	if len(args) < 1 {
		fmt.Println("Usage: pontusctl add-backend <address> [role]")
		os.Exit(1)
	}
	addr := args[0]
	role := "primary"
	if len(args) > 1 {
		role = args[1]
	}

	_, err := client.AddBackend(ctx, connect.NewRequest(&endpoints.AddBackendRequest{
		Config: &domain.BackendConfig{
			Address: addr,
			Role:    role,
		},
	}))
	if err != nil {
		log.Fatalf("Failed to add backend: %v", err)
	}
	fmt.Printf("Backend %s (%s) added successfully\n", addr, role)
}

func handleRemoveBackend(ctx context.Context, client serviceconnect.ManagementServiceClient, args []string) {
	if len(args) < 1 {
		fmt.Println("Usage: pontusctl remove-backend <address>")
		os.Exit(1)
	}
	addr := args[0]

	_, err := client.RemoveBackend(ctx, connect.NewRequest(&endpoints.RemoveBackendRequest{
		Address: addr,
	}))
	if err != nil {
		log.Fatalf("Failed to remove backend: %v", err)
	}
	fmt.Printf("Backend %s removed successfully\n", addr)
}

func handleDrain(ctx context.Context, client serviceconnect.ManagementServiceClient, args []string) {
	if len(args) < 1 {
		fmt.Println("Usage: pontusctl drain <address>")
		os.Exit(1)
	}
	addr := args[0]

	// Drain is currently a RemoveBackend followed by a wait (or we can implement a specific Drain flag/API)
	// For now, let's use UpdateBackend to mark it as draining if we had that state,
	// or just remove it from the balancer.
	fmt.Printf("Draining %s (removing from balancer)...\n", addr)
	handleRemoveBackend(ctx, client, args)
}

func handleProvisionReplica(ctx context.Context, client serviceconnect.ManagementServiceClient, args []string) {
	if len(args) < 4 {
		fmt.Println("Usage: pontusctl provision-replica <source_addr> <target_addr> <user> <password>")
		os.Exit(1)
	}

	stream, err := client.ProvisionReplica(ctx, connect.NewRequest(&endpoints.ProvisionReplicaRequest{
		SourceAddress:       args[0],
		TargetAddress:       args[1],
		ReplicationUser:     args[2],
		ReplicationPassword: args[3],
	}))
	if err != nil {
		log.Fatalf("Failed to start provisioning: %v", err)
	}

	for stream.Receive() {
		msg := stream.Msg()
		fmt.Printf("[%d%%] %s: %s\n", msg.Percentage, msg.Stage, msg.Message)
	}

	if err := stream.Err(); err != nil {
		log.Fatalf("Provisioning failed: %v", err)
	}

	fmt.Println("Successfully provisioned new replica.")
}

func handleLogs(ctx context.Context, client serviceconnect.ManagementServiceClient, args []string) {
	minLevel := "info"
	if len(args) > 0 {
		minLevel = args[0]
	}

	stream, err := client.StreamLogs(ctx, connect.NewRequest(&endpoints.StreamLogsRequest{
		MinLevel: minLevel,
	}))
	if err != nil {
		log.Fatalf("Failed to stream logs: %v", err)
	}

	fmt.Printf("Streaming logs (level: %s)...\n", minLevel)
	for stream.Receive() {
		msg := stream.Msg()
		ts := msg.Timestamp.AsTime().Format("2006-01-02 15:04:05")
		fmt.Printf("[%s] %s: %s\n", ts, msg.Level, msg.Message)
		for k, v := range msg.Attributes {
			fmt.Printf("  %s=%s", k, v)
		}
		if len(msg.Attributes) > 0 {
			fmt.Println()
		}
	}

	if err := stream.Err(); err != nil {
		log.Fatalf("Log stream failed: %v", err)
	}
}

func handleLogin(ctx context.Context, client serviceconnect.ManagementServiceClient, args []string) {
	if len(args) < 2 {
		fmt.Println("Usage: pontusctl login <username> <password>")
		os.Exit(1)
	}

	resp, err := client.Login(ctx, connect.NewRequest(&endpoints.LoginRequest{
		Username: args[0],
		Password: args[1],
	}))
	if err != nil {
		log.Fatalf("Login failed: %v", err)
	}

	fmt.Printf("Login successful!\n")
	fmt.Printf("Token: %s\n", resp.Msg.Token)
	fmt.Printf("Role: %s\n", resp.Msg.Role)
	fmt.Println("\nYou can set this token as PONTUS_TOKEN environment variable:")
	fmt.Printf("export PONTUS_TOKEN=%s\n", resp.Msg.Token)
}

func handleCreateUserAPI(ctx context.Context, client serviceconnect.ManagementServiceClient, token string, args []string) {
	if len(args) < 2 {
		fmt.Println("Usage: pontusctl create-user <username> <password> [role]")
		os.Exit(1)
	}

	username := args[0]
	password := args[1]
	role := "viewer"
	if len(args) > 2 {
		role = args[2]
	}

	req := connect.NewRequest(&endpoints.CreateUserRequest{
		Username: username,
		Password: password,
		Role:     role,
	})

	if token != "" {
		req.Header().Set("Authorization", "Bearer "+token)
	}

	resp, err := client.CreateUser(ctx, req)
	if err != nil {
		log.Fatalf("Failed to create user: %v", err)
	}

	fmt.Printf("User %s (%s) created successfully via API\n", resp.Msg.Username, resp.Msg.Role)
}
