package main

import (
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/storage"
	"log/slog"
	"os"
	"time"
)

func runAttachmentMaintenanceCommand(mode string) int {
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		slog.Error("attachment maintenance database unavailable")
		return 1
	}
	defer pool.Close()
	report, err := service.RunAttachmentStorageMaintenance(ctx, pool, storage.NewAttachmentStorageFromEnv(), mode)
	if err != nil {
		slog.Error("attachment maintenance failed; local data retained", "error", err)
		return 1
	}
	_ = json.NewEncoder(os.Stdout).Encode(report)
	return 0
}
