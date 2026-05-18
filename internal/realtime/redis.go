package realtime

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/redis/go-redis/v9"
)

const redisChannelPrefix = "pm:ws:"

type RedisBridge struct {
	client *redis.Client
	hub    *Hub
}

func NewRedisBridge(client *redis.Client, hub *Hub) *RedisBridge {
	return &RedisBridge{client: client, hub: hub}
}

func (b *RedisBridge) Publish(ctx context.Context, userID string, push WorkspacePush) error {
	payload, err := json.Marshal(push)
	if err != nil {
		return err
	}
	return b.client.Publish(ctx, redisChannelPrefix+userID, payload).Err()
}

func (b *RedisBridge) RunSubscriber(ctx context.Context) {
	pubsub := b.client.PSubscribe(ctx, redisChannelPrefix+"*")
	defer pubsub.Close()

	ch := pubsub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			userID := msg.Channel[len(redisChannelPrefix):]
			var push WorkspacePush
			if err := json.Unmarshal([]byte(msg.Payload), &push); err != nil {
				slog.Debug("redis ws payload", "err", err)
				continue
			}
			b.hub.Broadcast(userID, push)
		}
	}
}
