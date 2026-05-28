package config

import (
	"encoding/json"
	"fmt"
	"time"
)

const (
	// etcd key paths — single source of truth for the whole cluster
	KeyShardMap    = "/scheduler/config/shard_map"
	KeyNumShards   = "/scheduler/config/num_shards"
	KeyLiveNodes   = "/scheduler/members/"      // prefix — one key per live scheduler
	KeyLeader      = "/scheduler/leader"
	KeyAssignments = "/scheduler/assignments"   // shard_id -> scheduler_id

	// Timing wheel parameters
	WheelSlots       = 60
	WheelTickInterval = time.Second
	RefillWindow     = 5 * time.Minute   // load jobs due within this window into wheel
	RefillInterval   = 4 * time.Minute   // how often to refill from DB

	// etcd lease TTL for live node heartbeat
	// if a scheduler stops renewing, etcd expires the key after this duration
	NodeLeaseTTL = 10 // seconds
	HeartbeatInterval = 3 * time.Second
)

// ShardInfo describes which physical Postgres instance owns a logical shard.
type ShardInfo struct {
	ShardID  int    `json:"shard_id"`
	Host     string `json:"host"`     // e.g. "pg-0.internal:5432"
	Database string `json:"database"` // e.g. "jobs"
}

// ShardMap is the complete shard-to-instance mapping stored in etcd.
// Loaded once at startup, then kept fresh via etcd watch.
type ShardMap struct {
	NumShards int                  `json:"num_shards"`
	Shards    map[int]ShardInfo    `json:"shards"` // shard_id -> info
}

// BootstrapShardMap builds the initial shard map from a list of PG hosts
// and writes it to etcd. Run once at cluster initialisation — not on every start.
//
// Example: BootstrapShardMap(client, []string{"pg-0:5432", "pg-1:5432"}, 100)
// produces shard_id 0,2,4... → pg-0  and  1,3,5... → pg-1
func BootstrapShardMap(hosts []string, numShards int) (*ShardMap, error) {
	if len(hosts) == 0 {
		return nil, fmt.Errorf("at least one host required")
	}
	if numShards < len(hosts) {
		return nil, fmt.Errorf("num_shards (%d) must be >= num_hosts (%d)", numShards, len(hosts))
	}

	sm := &ShardMap{
		NumShards: numShards,
		Shards:    make(map[int]ShardInfo, numShards),
	}

	for shardID := 0; shardID < numShards; shardID++ {
		host := hosts[shardID%len(hosts)] // round-robin across instances
		sm.Shards[shardID] = ShardInfo{
			ShardID:  shardID,
			Host:     host,
			Database: "jobs",
		}
	}

	return sm, nil
}

func (sm *ShardMap) ToJSON() ([]byte, error) {
	return json.Marshal(sm)
}

func ShardMapFromJSON(data []byte) (*ShardMap, error) {
	var sm ShardMap
	if err := json.Unmarshal(data, &sm); err != nil {
		return nil, err
	}
	return &sm, nil
}

// Job is the core domain object stored in Postgres and passed through Kafka.
type Job struct {
	JobID       string          `json:"job_id"`
	TenantID    string          `json:"tenant_id"`
	ShardID     int             `json:"shard_id"`
	CronExpr    string          `json:"cron_expr"`
	Payload     json.RawMessage `json:"payload"`
	NextFireAt  time.Time       `json:"next_fire_at"`
	LastFireAt  *time.Time      `json:"last_fire_at,omitempty"`
	Enabled     bool            `json:"enabled"`
	TimeZone    string          `json:"timezone"`   // e.g. "Asia/Kolkata"
	MaxRetries  int             `json:"max_retries"`
	TimeoutSecs int             `json:"timeout_secs"`
}

// JobEvent is what gets published to Kafka when a job fires.
// The Key field is used for Kafka deduplication — same key = same message.
type JobEvent struct {
	JobID          string          `json:"job_id"`
	TenantID       string          `json:"tenant_id"`
	ScheduledEpoch int64           `json:"scheduled_epoch"` // unix seconds of intended fire time
	Payload        json.RawMessage `json:"payload"`
	AttemptNumber  int             `json:"attempt_number"`
}

// DeduplicationKey returns the idempotency key used as the Kafka message key.
// Same job + same scheduled time always produces the same key.
// Even if a job fires twice (leader failover race), Kafka deduplicates it.
func (e *JobEvent) DeduplicationKey() string {
	return fmt.Sprintf("%s:%d", e.JobID, e.ScheduledEpoch)
}
