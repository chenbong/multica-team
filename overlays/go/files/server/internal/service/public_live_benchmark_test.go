package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Explicit operational opt-in only. No live database is accessed by default
// tests. EXPLAIN runs the exact production SELECT inside READ ONLY + ROLLBACK.
func TestPublicLiveReadOnlyBenchmark(t *testing.T) {
	if os.Getenv("PUBLIC_LIVE_READONLY_BENCHMARK") != "1" {
		t.Skip("explicit read-only benchmark only")
	}
	var source struct {
		Host      string `json:"host"`
		Port      int    `json:"port"`
		Docker    string `json:"docker"`
		Container string `json:"container"`
		Database  string `json:"database"`
	}
	raw, e := os.ReadFile(os.Getenv("PUBLIC_LIVE_BENCHMARK_CONFIG"))
	if e != nil || json.Unmarshal(raw, &source) != nil {
		t.Fatal("protected source configuration unavailable")
	}
	if strings.HasPrefix(source.Host, "-") || !regexp.MustCompile(`^[a-zA-Z0-9._@:-]+$`).MatchString(source.Host) || source.Port < 1 || source.Port > 65535 || !regexp.MustCompile(`^/[a-zA-Z0-9/._-]+$`).MatchString(source.Docker) || !regexp.MustCompile(`^[-a-zA-Z0-9_.]+$`).MatchString(source.Container) || !regexp.MustCompile(`^\w+$`).MatchString(source.Database) {
		t.Fatal("invalid protected source configuration")
	}
	command := fmt.Sprintf(`%s exec -i %s sh -ec 'exec psql -XqAt -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$1"' live-stats %s`, source.Docker, source.Container, source.Database)
	for _, section := range []string{"realtime", "recent", "history"} {
		query, e := publicLiveSQL(section)
		if e != nil {
			t.Fatal(e)
		}
		// Measure the active/PV subqueries even before their first complete
		// coverage window. This changes only this read-only EXPLAIN, never
		// stored configuration or data exposed by the running platform.
		if os.Getenv("PUBLIC_LIVE_BENCHMARK_ASSUME_COVERAGE") == "1" {
			query = regexp.MustCompile(`CASE WHEN \(SELECT collection_started_at FROM live_dashboard_config WHERE id=1\)<=.*? THEN`).ReplaceAllString(query, "CASE WHEN TRUE THEN")
		}
		for sample := 1; sample <= 3; sample++ {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			cmd := exec.CommandContext(ctx, "ssh", "-p", strconv.Itoa(source.Port), "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "ConnectTimeout=5", source.Host, command)
			cmd.Stdin = strings.NewReader("BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY;\nSET LOCAL statement_timeout='8s';\nSET LOCAL lock_timeout='1s';\nEXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) " + query + ";\nROLLBACK;\n")
			output, e := cmd.Output()
			cancel()
			if e != nil {
				t.Fatalf("bounded read-only benchmark failed: %s sample %d", section, sample)
			}
			var result []struct {
				Execution float64 `json:"Execution Time"`
				Planning  float64 `json:"Planning Time"`
				Plan      struct {
					Hits  int64 `json:"Shared Hit Blocks"`
					Reads int64 `json:"Shared Read Blocks"`
				} `json:"Plan"`
			}
			if json.Unmarshal(output, &result) != nil || len(result) != 1 {
				t.Fatal("invalid query timing result")
			}
			t.Logf("BENCH section=%s sample=%d execution_ms=%.3f planning_ms=%.3f shared_hits=%d shared_reads=%d", section, sample, result[0].Execution, result[0].Planning, result[0].Plan.Hits, result[0].Plan.Reads)
		}
	}
}
