package service

import (
	"context"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Plugin upgrades must not modify or prune skills created by another account.
func requirePluginSkillCreator(ctx context.Context, q *db.Queries, installation, user pgtype.UUID) error {
	skills, err := q.ListPluginSkills(ctx, installation)
	if err != nil {
		return &PluginError{Kind: PluginErrorUnavailable, Message: "load plugin skills", Err: err}
	}
	for _, skill := range skills {
		if !skill.CreatedBy.Valid || skill.CreatedBy != user {
			return pluginErrf(PluginErrorForbidden, "only the skill creator can update these plugin skills")
		}
	}
	return nil
}

// The HTTP caller identity is revalidated inside plugin uninstall's transaction.
// Trusted internal callers retain the original service API.
type skillMutationUserKey struct{}

func WithSkillMutationUser(ctx context.Context, user pgtype.UUID) context.Context {
	return context.WithValue(ctx, skillMutationUserKey{}, user)
}
