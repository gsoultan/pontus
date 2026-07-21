package orchestration

import (
	"context"
	"fmt"

	"github.com/gsoultan/pontus/api/proto/endpoints"
	"github.com/gsoultan/pontus/api/proto/service"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

// AgentClient defines the interface for communicating with a Pontus Agent.
type AgentClient interface {
	GetSystemInfo(ctx context.Context) (*endpoints.GetSystemInfoResponse, error)
	UpdateConfig(ctx context.Context, filePath, content string) error
	ExecuteCommand(ctx context.Context, command string, args []string, env map[string]string) (<-chan *endpoints.ExecuteCommandResponse, error)
	RestartService(ctx context.Context, serviceName string) error
	SetupReplication(ctx context.Context, req *endpoints.SetupReplicationRequest) (<-chan *endpoints.ReplicationProgress, error)
	InitializeDatabase(ctx context.Context, req *endpoints.InitializeDatabaseRequest) (service.AgentService_InitializeDatabaseClient, error)
	InstallDatabase(ctx context.Context, req *endpoints.InstallDatabaseRequest) (service.AgentService_InstallDatabaseClient, error)
	BackupDatabase(ctx context.Context, req *endpoints.BackupDatabaseRequest) (service.AgentService_BackupDatabaseClient, error)
	RestoreDatabase(ctx context.Context, req *endpoints.RestoreDatabaseRequest) (service.AgentService_RestoreDatabaseClient, error)
	PromoteNode(ctx context.Context, req *endpoints.PromoteNodeRequest) (*endpoints.PromoteNodeResponse, error)
	RemoveDatabase(ctx context.Context, req *endpoints.RemoveDatabaseRequest) (*endpoints.RemoveDatabaseResponse, error)
	Close() error
}

type agentClient struct {
	conn   *grpc.ClientConn
	client service.AgentServiceClient
}

// NewAgentClient creates a new client for a Pontus Agent.
func NewAgentClient(addr string, token string) (AgentClient, error) {
	var opts []grpc.DialOption
	opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))

	if token != "" {
		opts = append(opts, grpc.WithUnaryInterceptor(tokenClientInterceptor(token)), grpc.WithStreamInterceptor(tokenStreamClientInterceptor(token)))
	}

	conn, err := grpc.NewClient(addr, opts...)
	if err != nil {
		return nil, err
	}

	return &agentClient{
		conn:   conn,
		client: service.NewAgentServiceClient(conn),
	}, nil
}

func tokenClientInterceptor(token string) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", token)
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

func tokenStreamClientInterceptor(token string) grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", token)
		return streamer(ctx, desc, cc, method, opts...)
	}
}

func (c *agentClient) GetSystemInfo(ctx context.Context) (*endpoints.GetSystemInfoResponse, error) {
	return c.client.GetSystemInfo(ctx, &endpoints.GetSystemInfoRequest{})
}

func (c *agentClient) UpdateConfig(ctx context.Context, filePath, content string) error {
	resp, err := c.client.UpdateConfig(ctx, &endpoints.UpdateConfigRequest{
		FilePath: filePath,
		Content:  content,
	})
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("failed to update config: %s", resp.ErrorMessage)
	}
	return nil
}

func (c *agentClient) ExecuteCommand(ctx context.Context, command string, args []string, env map[string]string) (<-chan *endpoints.ExecuteCommandResponse, error) {
	stream, err := c.client.ExecuteCommand(ctx, &endpoints.ExecuteCommandRequest{
		Command: command,
		Args:    args,
		Env:     env,
	})
	if err != nil {
		return nil, err
	}

	out := make(chan *endpoints.ExecuteCommandResponse)
	go func() {
		defer close(out)
		for {
			msg, err := stream.Recv()
			if err != nil {
				return
			}
			out <- msg
		}
	}()

	return out, nil
}

func (c *agentClient) RestartService(ctx context.Context, serviceName string) error {
	resp, err := c.client.RestartService(ctx, &endpoints.RestartServiceRequest{
		ServiceName: serviceName,
	})
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("failed to restart service: %s", resp.ErrorMessage)
	}
	return nil
}

func (c *agentClient) SetupReplication(ctx context.Context, req *endpoints.SetupReplicationRequest) (<-chan *endpoints.ReplicationProgress, error) {
	stream, err := c.client.SetupReplication(ctx, req)
	if err != nil {
		return nil, err
	}

	out := make(chan *endpoints.ReplicationProgress)
	go func() {
		defer close(out)
		for {
			msg, err := stream.Recv()
			if err != nil {
				return
			}
			out <- msg
		}
	}()

	return out, nil
}

func (c *agentClient) InitializeDatabase(ctx context.Context, req *endpoints.InitializeDatabaseRequest) (service.AgentService_InitializeDatabaseClient, error) {
	return c.client.InitializeDatabase(ctx, req)
}

func (c *agentClient) InstallDatabase(ctx context.Context, req *endpoints.InstallDatabaseRequest) (service.AgentService_InstallDatabaseClient, error) {
	return c.client.InstallDatabase(ctx, req)
}

func (c *agentClient) BackupDatabase(ctx context.Context, req *endpoints.BackupDatabaseRequest) (service.AgentService_BackupDatabaseClient, error) {
	return c.client.BackupDatabase(ctx, req)
}

func (c *agentClient) RestoreDatabase(ctx context.Context, req *endpoints.RestoreDatabaseRequest) (service.AgentService_RestoreDatabaseClient, error) {
	return c.client.RestoreDatabase(ctx, req)
}

func (c *agentClient) PromoteNode(ctx context.Context, req *endpoints.PromoteNodeRequest) (*endpoints.PromoteNodeResponse, error) {
	return c.client.PromoteNode(ctx, req)
}

func (c *agentClient) RemoveDatabase(ctx context.Context, req *endpoints.RemoveDatabaseRequest) (*endpoints.RemoveDatabaseResponse, error) {
	return c.client.RemoveDatabase(ctx, req)
}

func (c *agentClient) Close() error {
	return c.conn.Close()
}
