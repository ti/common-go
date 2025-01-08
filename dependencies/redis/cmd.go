package redis

import (
	"context"
	"time"

	"github.com/redis/rueidis"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Set the redis set method
func (r *Redis) Set(ctx context.Context, key, value string, expiration time.Duration) error {
	cmd := r.client.B().Set().Key(key).Value(value).Ex(expiration).Build()
	return r.client.Do(ctx, cmd).Error()
}

// Get the redis get method
func (r *Redis) Get(ctx context.Context, key string) (string, error) {
	cmd := r.client.B().Get().Key(key).Build()
	data, err := r.client.Do(ctx, cmd).ToString()
	if err != nil {
		if rueidis.IsRedisNil(err) {
			return "", status.Error(codes.NotFound, err.Error())
		}
		return "", err
	}
	return data, nil
}

// Delete the redis delete method
func (r *Redis) Delete(ctx context.Context, key ...string) error {
	cmd := r.client.B().Del().Key(key...).Build()
	return r.client.Do(ctx, cmd).Error()
}
