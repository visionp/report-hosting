package repository

import (
	"context"
	"errors"
	"testing"

	"report_hosting/internal/domain"
)

func TestDeleteSiteWithAuditAtomic(t *testing.T) {
	for _, failure := range []string{"none", "audit", "delete"} {
		t.Run(failure, func(t *testing.T) {
			s := testStore(t)
			ctx := context.Background()
			must(t, s.CreateSite(ctx, site("a", "demo")))
			must(t, s.AddRelease(ctx, release("r1", "a", 1)))
			switch failure {
			case "audit":
				_, err := s.db.Exec(`CREATE TRIGGER fail_audit BEFORE INSERT ON audit_events BEGIN SELECT RAISE(FAIL,'audit unavailable'); END`)
				must(t, err)
			case "delete":
				_, err := s.db.Exec(`CREATE TRIGGER fail_delete BEFORE DELETE ON sites BEGIN SELECT RAISE(FAIL,'delete unavailable'); END`)
				must(t, err)
			}
			err := s.DeleteSiteWithAudit(ctx, "a", "administrator")
			if failure == "none" {
				must(t, err)
			} else if err == nil {
				t.Fatal("expected transaction failure")
			}
			_, getErr := s.GetSite(ctx, "a")
			releases, err := s.ListReleases(ctx, "a")
			must(t, err)
			audit, err := s.ListAudit(ctx, "a")
			must(t, err)
			if failure == "none" {
				if !errors.Is(getErr, domain.ErrNotFound) || len(releases) != 0 || len(audit) != 1 || audit[0].Action != "delete" || audit[0].Actor != "administrator" {
					t.Fatalf("inconsistent committed deletion: site=%v releases=%v audit=%v", getErr, releases, audit)
				}
				if err = s.DeleteSiteWithAudit(ctx, "a", "administrator"); !errors.Is(err, domain.ErrNotFound) {
					t.Fatalf("retry: %v", err)
				}
				audit, err = s.ListAudit(ctx, "a")
				must(t, err)
				if len(audit) != 1 {
					t.Fatal("duplicate delete audit")
				}
			} else if getErr != nil || len(releases) != 1 || len(audit) != 0 {
				t.Fatalf("transaction did not roll back: site=%v releases=%v audit=%v", getErr, releases, audit)
			}
		})
	}
}
