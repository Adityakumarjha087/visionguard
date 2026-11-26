// Package bus is the Redis contract shared with the inference service (see ml/src/visionguard_ml/bus.py).
package bus

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"visionguard/backend/internal/domain"
)

const (
	ConfigKey     = "vg:config"
	FramesStream  = "vg:frames"
	EventsStream  = "vg:events"
	ModelsKey     = "vg:models"
	StatusPrefix  = "vg:status:"
	ConsumerGroup = "api"
)

// Bus wraps a Redis client.
type Bus struct {
	R *redis.Client
}

// New connects to Redis from a URL.
func New(url string) (*Bus, error) {
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, err
	}
	return &Bus{R: redis.NewClient(opts)}, nil
}

// Ping checks connectivity.
func (b *Bus) Ping(ctx context.Context) error { return b.R.Ping(ctx).Err() }

// ---------------------------------------------------------------- desired state

// CameraConfig is one camera entry in the desired-state document.
type CameraConfig struct {
	domain.Camera
	Zones []domain.Zone `json:"zones"`
	Rules []domain.Rule `json:"rules"`
}

// ConfigDoc is written to vg:config and polled by the inference service.
type ConfigDoc struct {
	Version     int64          `json:"version"`
	ActiveModel string         `json:"active_model"`
	Cameras     []CameraConfig `json:"cameras"`
}

// PublishConfig writes the desired-state document.
func (b *Bus) PublishConfig(ctx context.Context, doc ConfigDoc) error {
	raw, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	return b.R.Set(ctx, ConfigKey, raw, 0).Err()
}

// Statuses fetches live camera statuses (missing/expired -> "offline").
func (b *Bus) Statuses(ctx context.Context, ids []string) (map[string]domain.CameraStatus, error) {
	out := make(map[string]domain.CameraStatus, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	keys := make([]string, len(ids))
	for i, id := range ids {
		keys[i] = StatusPrefix + id
	}
	vals, err := b.R.MGet(ctx, keys...).Result()
	if err != nil {
		return out, err
	}
	for i, v := range vals {
		st := domain.CameraStatus{State: "offline"}
		if s, ok := v.(string); ok {
			if err := json.Unmarshal([]byte(s), &st); err != nil {
				st = domain.CameraStatus{State: "offline"}
			}
		}
		out[ids[i]] = st
	}
	return out, nil
}

// Models returns the catalogue advertised by the inference service (may be empty).
func (b *Bus) Models(ctx context.Context) ([]domain.Model, error) {
	raw, err := b.R.Get(ctx, ModelsKey).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var models []domain.Model
	return models, json.Unmarshal(raw, &models)
}

// ---------------------------------------------------------------- consumers

// Event is one entry from vg:events.
type Event struct {
	ID   string
	Type string
	Data json.RawMessage
}

// Handler processes an event; returning an error leaves it pending for redelivery.
type Handler func(ctx context.Context, ev Event) error

// ConsumeEvents runs a consumer-group loop on vg:events until ctx is done.
// Delivery is at-least-once: handlers must be idempotent.
func (b *Bus) ConsumeEvents(ctx context.Context, consumer string, h Handler) {
	if err := b.R.XGroupCreateMkStream(ctx, EventsStream, ConsumerGroup, "$").Err(); err != nil &&
		!strings.Contains(err.Error(), "BUSYGROUP") {
		slog.Error("create consumer group", "err", err)
	}
	// First drain this consumer's own pending entries (crash recovery), then new ones.
	start := "0"
	for ctx.Err() == nil {
		res, err := b.R.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group: ConsumerGroup, Consumer: consumer, Streams: []string{EventsStream, start},
			Count: 200, Block: 2 * time.Second,
		}).Result()
		if err != nil {
			if errors.Is(err, redis.Nil) || ctx.Err() != nil {
				if start == "0" {
					start = ">"
				}
				continue
			}
			if strings.Contains(err.Error(), "NOGROUP") {
				_ = b.R.XGroupCreateMkStream(ctx, EventsStream, ConsumerGroup, "$").Err()
			}
			slog.Warn("xreadgroup failed", "err", err)
			sleep(ctx, time.Second)
			continue
		}
		delivered := 0
		for _, stream := range res {
			for _, msg := range stream.Messages {
				delivered++
				ev := Event{ID: msg.ID, Type: str(msg.Values["type"]), Data: json.RawMessage(str(msg.Values["data"]))}
				if err := h(ctx, ev); err != nil {
					slog.Warn("event handler failed", "type", ev.Type, "id", ev.ID, "err", err)
					continue
				}
				b.R.XAck(ctx, EventsStream, ConsumerGroup, msg.ID)
			}
		}
		if start == "0" && delivered == 0 {
			start = ">"
		}
	}
}

// FrameHandler receives raw frame JSON and its camera id.
type FrameHandler func(cameraID string, raw []byte)

// TailFrames follows vg:frames from "now" (ephemeral live overlay data, no consumer group).
func (b *Bus) TailFrames(ctx context.Context, h FrameHandler) {
	last := "$"
	for ctx.Err() == nil {
		res, err := b.R.XRead(ctx, &redis.XReadArgs{Streams: []string{FramesStream, last}, Count: 500, Block: 2 * time.Second}).Result()
		if err != nil {
			if !errors.Is(err, redis.Nil) && ctx.Err() == nil {
				slog.Warn("xread frames failed", "err", err)
				sleep(ctx, time.Second)
			}
			continue
		}
		for _, stream := range res {
			for _, msg := range stream.Messages {
				last = msg.ID
				raw := str(msg.Values["data"])
				var head struct {
					CameraID string `json:"camera_id"`
				}
				if json.Unmarshal([]byte(raw), &head) == nil && head.CameraID != "" {
					h(head.CameraID, []byte(raw))
				}
			}
		}
	}
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
