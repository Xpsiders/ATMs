package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"attendance-api/internal/catalogue"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

type auditExecer interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

func auditAcademicResource(
	ctx context.Context,
	execer auditExecer,
	tenant, actor pgtype.UUID,
	resource catalogue.Resource,
	action string,
	resourceID any,
	fields map[string]any,
) error {
	details := []byte(`{}`)
	if fields != nil {
		var err error
		details, err = json.Marshal(fields)
		if err != nil {
			return fmt.Errorf("encode %s audit details: %w", resource, err)
		}
	}
	name := strings.TrimSuffix(string(resource), "s")
	resourceType := strings.ReplaceAll(name, "-", "_")
	_, err := execer.Exec(ctx, `INSERT INTO audit_log(
		institution_id,actor_user_id,action,resource_type,resource_id,details
	) VALUES($1,$2,$3,$4,$5::text,$6::jsonb)`,
		tenant, actor, resourceType+"."+action, resourceType, resourceID, details)
	if err != nil {
		return fmt.Errorf("audit %s %s: %w", resource, action, err)
	}
	return nil
}
