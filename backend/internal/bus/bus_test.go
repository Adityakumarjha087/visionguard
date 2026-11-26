package bus_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"visionguard/backend/internal/bus"
	"visionguard/backend/internal/testutil"
)

func TestStatusesModelsAndConfig(t *testing.T) {
	b, _ := testutil.Redis(t)
	ctx := context.Background()
	require.NoError(t, b.Ping(ctx))
	require.NoError(t, b.R.Set(ctx, bus.StatusPrefix+"a", `{"state":"online","fps":10}`, 0).Err())
	require.NoError(t, b.R.Set(ctx, bus.StatusPrefix+"c", `not-json`, 0).Err())
	st, err := b.Statuses(ctx, []string{"a", "b", "c"})
	require.NoError(t, err)
	assert.Equal(t, "online", st["a"].State)
	assert.Equal(t, "offline", st["b"].State)
	assert.Equal(t, "offline", st["c"].State)
	empty, err := b.Statuses(ctx, nil)
	require.NoError(t, err)
	assert.Empty(t, empty)

	models, err := b.Models(ctx)
	require.NoError(t, err)
	assert.Nil(t, models)
	require.NoError(t, b.R.Set(ctx, bus.ModelsKey, `[{"id":"m","available":true}]`, 0).Err())
	models, err = b.Models(ctx)
	require.NoError(t, err)
	assert.Equal(t, "m", models[0].ID)

	require.NoError(t, b.PublishConfig(ctx, bus.ConfigDoc{Version: 7, ActiveModel: "x"}))
	raw, _ := b.R.Get(ctx, bus.ConfigKey).Bytes()
	var doc bus.ConfigDoc
	require.NoError(t, json.Unmarshal(raw, &doc))
	assert.Equal(t, int64(7), doc.Version)

	_, err = bus.New("not a url")
	assert.Error(t, err)
}

func TestConsumeEventsAcksAndRedelivers(t *testing.T) {
	b, _ := testutil.Redis(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	seen := map[string]int{}
	failOnce := true
	go b.ConsumeEvents(ctx, "c1", func(_ context.Context, ev bus.Event) error {
		mu.Lock()
		defer mu.Unlock()
		seen[ev.Type]++
		if ev.Type == "flaky" && failOnce {
			failOnce = false
			return assert.AnError
		}
		return nil
	})
	time.Sleep(100 * time.Millisecond) // group created at "$"
	for _, typ := range []string{"alert", "stats", "flaky"} {
		require.NoError(t, b.R.XAdd(ctx, &redis.XAddArgs{Stream: bus.EventsStream, Values: map[string]any{"type": typ, "data": "{}"}}).Err())
	}
	assert.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return seen["alert"] == 1 && seen["stats"] == 1 && seen["flaky"] == 1
	}, 5*time.Second, 20*time.Millisecond)
	pending, err := b.R.XPending(ctx, bus.EventsStream, bus.ConsumerGroup).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(1), pending.Count) // the failed one stays pending for redelivery
}

func TestTailFrames(t *testing.T) {
	b, _ := testutil.Redis(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got := make(chan string, 4)
	go b.TailFrames(ctx, func(cam string, raw []byte) { got <- cam })
	time.Sleep(100 * time.Millisecond)
	require.NoError(t, b.R.XAdd(ctx, &redis.XAddArgs{Stream: bus.FramesStream, Values: map[string]any{"data": `{"camera_id":"cam-1"}`}}).Err())
	require.NoError(t, b.R.XAdd(ctx, &redis.XAddArgs{Stream: bus.FramesStream, Values: map[string]any{"data": `garbage`}}).Err())
	select {
	case cam := <-got:
		assert.Equal(t, "cam-1", cam)
	case <-time.After(5 * time.Second):
		t.Fatal("no frame received")
	}
}
