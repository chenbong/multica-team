package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisLocalMemoryStore mirrors RedisLocalSkillListStore for runtime-local
// agent memory requests: one JSON record per request plus a per-runtime
// pending ZSET, claimed atomically through claimPendingScript.

const (
	localMemoryKeyPrefix     = "mul:" + runtimePendingRedisHashTag + ":local_memory:"
	localMemoryPendingPrefix = "mul:" + runtimePendingRedisHashTag + ":local_memory:pending:"
)

func localMemoryKey(id string) string               { return localMemoryKeyPrefix + id }
func localMemoryPendingKey(runtimeID string) string { return localMemoryPendingPrefix + runtimeID }

// runtimeLocalMemoryEnvelope keeps RunStartedAt (json:"-" on the API type).
type runtimeLocalMemoryEnvelope struct {
	Request      *RuntimeLocalMemoryRequest `json:"request"`
	RunStartedAt *time.Time                 `json:"run_started_at,omitempty"`
}

type RedisLocalMemoryStore struct {
	rdb redis.UniversalClient
}

func NewRedisLocalMemoryStore(rdb redis.UniversalClient) *RedisLocalMemoryStore {
	return &RedisLocalMemoryStore{rdb: rdb}
}

func encodeLocalMemoryRequest(req *RuntimeLocalMemoryRequest) ([]byte, error) {
	data, err := json.Marshal(runtimeLocalMemoryEnvelope{Request: req, RunStartedAt: req.RunStartedAt})
	if err != nil {
		return nil, fmt.Errorf("marshal local memory request: %w", err)
	}
	return data, nil
}

func (s *RedisLocalMemoryStore) Create(ctx context.Context, runtimeID, provider, kind string, keys []string) (*RuntimeLocalMemoryRequest, error) {
	req := newRuntimeLocalMemoryRequest(runtimeID, provider, kind, keys)
	data, err := encodeLocalMemoryRequest(req)
	if err != nil {
		return nil, err
	}
	requestKey := localMemoryKey(req.ID)
	pendingKey := localMemoryPendingKey(runtimeID)
	pipe := s.rdb.Pipeline()
	pipe.Set(ctx, requestKey, data, runtimeLocalMemoryStoreRetention)
	pipe.ZAdd(ctx, pendingKey, redis.Z{Score: float64(req.CreatedAt.UnixNano()), Member: req.ID})
	pipe.Expire(ctx, pendingKey, runtimeLocalMemoryStoreRetention*2)
	if _, err := pipe.Exec(ctx); err != nil {
		_ = s.rdb.Del(ctx, requestKey).Err()
		_ = s.rdb.ZRem(ctx, pendingKey, req.ID).Err()
		return nil, fmt.Errorf("persist local memory request: %w", err)
	}
	return req, nil
}

func (s *RedisLocalMemoryStore) load(ctx context.Context, id string) (*RuntimeLocalMemoryRequest, error) {
	raw, err := s.rdb.Get(ctx, localMemoryKey(id)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get local memory request: %w", err)
	}
	var env runtimeLocalMemoryEnvelope
	if err := json.Unmarshal(raw, &env); err != nil || env.Request == nil {
		return nil, fmt.Errorf("decode local memory request: %v", err)
	}
	req := env.Request
	req.RunStartedAt = env.RunStartedAt
	if applyLocalMemoryTimeout(req, time.Now()) {
		if err := s.persist(ctx, req); err != nil {
			return nil, err
		}
		s.rdb.ZRem(ctx, localMemoryPendingKey(req.RuntimeID), req.ID)
	}
	return req, nil
}

func (s *RedisLocalMemoryStore) persist(ctx context.Context, req *RuntimeLocalMemoryRequest) error {
	data, err := encodeLocalMemoryRequest(req)
	if err != nil {
		return err
	}
	if err := s.rdb.Set(ctx, localMemoryKey(req.ID), data, runtimeLocalMemoryStoreRetention).Err(); err != nil {
		return fmt.Errorf("persist local memory request: %w", err)
	}
	return nil
}

func (s *RedisLocalMemoryStore) Get(ctx context.Context, id string) (*RuntimeLocalMemoryRequest, error) {
	return s.load(ctx, id)
}

func (s *RedisLocalMemoryStore) HasPending(ctx context.Context, runtimeID string) (bool, error) {
	cnt, err := s.rdb.ZCard(ctx, localMemoryPendingKey(runtimeID)).Result()
	if err != nil {
		return false, fmt.Errorf("zcard pending: %w", err)
	}
	return cnt > 0, nil
}

func (s *RedisLocalMemoryStore) PopPendingBatch(ctx context.Context, runtimeID string, limit int) ([]*RuntimeLocalMemoryRequest, error) {
	pendingKey := localMemoryPendingKey(runtimeID)
	var out []*RuntimeLocalMemoryRequest
	for attempts := 0; len(out) < limit && attempts < limit+localSkillRedisPopMaxRetries; attempts++ {
		ids, err := s.rdb.ZRange(ctx, pendingKey, 0, 0).Result()
		if err != nil {
			return out, fmt.Errorf("zrange pending: %w", err)
		}
		if len(ids) == 0 {
			break
		}
		id := ids[0]
		req, err := s.load(ctx, id)
		if err != nil {
			return out, err
		}
		if req == nil || req.Status != RuntimeLocalSkillPending {
			s.rdb.ZRem(ctx, pendingKey, id)
			continue
		}
		claimLocalMemoryRequest(req, time.Now())
		data, err := encodeLocalMemoryRequest(req)
		if err != nil {
			return out, err
		}
		won, err := claimPendingScript.Run(ctx, s.rdb,
			[]string{pendingKey, localMemoryKey(id)},
			id, data, int(runtimeLocalMemoryStoreRetention.Seconds()),
		).Int64()
		if err != nil {
			return out, fmt.Errorf("claim pending: %w", err)
		}
		if won == 1 {
			out = append(out, req)
		}
	}
	return out, nil
}

func (s *RedisLocalMemoryStore) Complete(ctx context.Context, id string, result RuntimeLocalMemoryResult) error {
	req, err := s.load(ctx, id)
	if err != nil || req == nil {
		return err
	}
	applyLocalMemoryResult(req, result)
	return s.persist(ctx, req)
}

func (s *RedisLocalMemoryStore) Fail(ctx context.Context, id string, errMsg string) error {
	req, err := s.load(ctx, id)
	if err != nil || req == nil {
		return err
	}
	req.Status = RuntimeLocalSkillFailed
	req.Error = errMsg
	req.UpdatedAt = time.Now()
	return s.persist(ctx, req)
}
