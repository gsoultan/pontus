package transport

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func withToken(token string) context.Context {
	return metadata.NewIncomingContext(context.Background(),
		metadata.Pairs("authorization", token))
}

// Every rejection has to be Unauthenticated, not a bare error: the management
// side distinguishes "wrong credentials" from "the agent is broken", and a
// mislabelled code sends an operator looking at the wrong host.
func TestAuthorizeRejectsBadCredentials(t *testing.T) {
	for name, ctx := range map[string]context.Context{
		"no metadata":  context.Background(),
		"no header":    metadata.NewIncomingContext(context.Background(), metadata.MD{}),
		"empty token":  withToken(""),
		"wrong token":  withToken("nope"),
		"prefix only":  withToken("secret"),
		"longer token": withToken("secret-token-extra"),
	} {
		t.Run(name, func(t *testing.T) {
			err := authorize(ctx, "secret-token")
			if err == nil {
				t.Fatal("authorize accepted the call")
			}
			if got := status.Code(err); got != codes.Unauthenticated {
				t.Errorf("code = %v, want Unauthenticated", got)
			}
		})
	}
}

func TestAuthorizeAcceptsTheToken(t *testing.T) {
	if err := authorize(withToken("secret-token"), "secret-token"); err != nil {
		t.Fatalf("authorize rejected the correct token: %v", err)
	}
}

// A server built with an empty token must not become a server that accepts
// every empty-token client. cmd/agent refuses to start in that case; this
// pins the interceptor's own behaviour so a future caller cannot reintroduce
// the hole by passing "".
func TestEmptyTokenStillRequiresAHeader(t *testing.T) {
	if err := authorize(context.Background(), ""); err == nil {
		t.Fatal("a call with no credentials was accepted against an empty token")
	}
}
