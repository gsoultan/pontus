package infrastructure

import (
	"testing"
	"time"

	"github.com/gsoultan/pontus/api/proto/domain"
	"github.com/gsoultan/pontus/api/proto/endpoints"
	"github.com/gsoultan/pontus/server/management/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestService_GetStatus(t *testing.T) {
	// Setup
	db, err := store.NewManagementDB(":memory:")
	if err != nil {
		t.Fatalf("failed to open management db: %v", err)
	}
	defer db.Close()

	pstore := store.NewSQLiteProject(db)
	ustore := store.NewSQLiteUser(db)
	sstore := store.NewSQLiteSetting(db)

	svc := NewService(t.Context(), pstore, ustore, sstore, 1*time.Second, nil, "test-secret")

	// 1. Test GetStatus with non-existent project
	_, err = svc.GetStatus(t.Context(), &endpoints.GetStatusRequest{
		ProjectId: "non-existent",
	})
	if err == nil {
		t.Error("expected error for non-existent project, got nil")
	}

	// 2. Test GetStatus with existing project but no proxies
	pcfg := &domain.Project{
		Id:   "p1",
		Name: "Project 1",
	}
	pstore.Upsert(pcfg)

	// We need to reload or manually add to projects map, but NewService loads from store.
	// Since we already called NewService, we should use CreateProject or re-init.
	svc = NewService(t.Context(), pstore, ustore, sstore, 1*time.Second, nil, "test-secret")

	resp, err := svc.GetStatus(t.Context(), &endpoints.GetStatusRequest{
		ProjectId: "p1",
	})

	if err == nil {
		t.Errorf("expected error for project with no proxies, got resp: %+v", resp)
	} else if status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound error code, got: %v", status.Code(err))
	}
}
