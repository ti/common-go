package mqlru

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"time"

	"github.com/google/uuid"
	ttlcache "github.com/jellydator/ttlcache/v3"
	"github.com/ti/common-go/dependencies/broker"
	"github.com/ti/common-go/log"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Lru the lru cache instance
type Lru struct {
	broker     *broker.Broker
	cache      *ttlcache.Cache[string, []byte]
	instanceID string
	topic      string
	disableMQ  bool
}

// New lru cache
func New(ctx context.Context, configURL string) (*Lru, error) {
	if configURL == "" {
		configURL = "cache://memory?ttl=5m&touch=false"
	}
	u, err := url.Parse(configURL)
	if err != nil {
		return nil, err
	}
	cache := &Lru{}
	return cache, cache.Init(ctx, u)
}

// Init by uri
func (l *Lru) Init(ctx context.Context, u *url.URL) error {
	query := u.Query()
	var opts []ttlcache.Option[string, []byte]
	if ttl, _ := time.ParseDuration(query.Get("ttl")); ttl > 0 {
		opts = append(opts, ttlcache.WithTTL[string, []byte](ttl))
	}
	if capacity, _ := strconv.ParseUint(query.Get("capacity"), 10, 64); capacity > 0 {
		opts = append(opts, ttlcache.WithCapacity[string, []byte](capacity))
	}
	const strFalse = "false"
	if query.Get("touch") == strFalse {
		opts = append(opts, ttlcache.WithDisableTouchOnHit[string, []byte]())
	}
	if query.Get("mq") == strFalse {
		l.disableMQ = true
	}
	l.cache = ttlcache.New[string, []byte](opts...)
	go l.cache.Start()
	if u.Host == "memory" {
		l.disableMQ = true
		return nil
	}
	if l.disableMQ {
		return nil
	}
	l.broker = &broker.Broker{}
	if err := l.broker.Init(ctx, u); err != nil {
		return err
	}
	if hostname, err := os.Hostname(); err == nil {
		if len(hostname) > 16 {
			hostname = hostname[len(hostname)-16:]
		}
		l.instanceID = hostname
	} else {
		l.instanceID = uuid.New().String()
	}
	l.topic = u.Path[1:]
	logger := log.Extract(ctx)
	err := l.broker.Subscribe(context.Background(),
		[]string{l.topic}, l.instanceID, func(publication broker.Publication) error {
			msg := publication.Message()
			instanceID := msg.Header["instance"]
			key := msg.Header["id"]
			logger.With(map[string]any{
				"action":  "lru.Subscribe",
				"referer": instanceID,
				"id":      key,
			}).Debug("received")
			if instanceID == l.instanceID {
				return nil
			}
			ttlStr := msg.Header["ttl"]
			ttl, errTTL := time.ParseDuration(ttlStr)
			if errTTL != nil {
				logger.With(map[string]any{
					"action":  "lru.Subscribe",
					"referer": key,
				}).Error(errTTL.Error())
				return nil
			}
			if ttl == 0 || len(msg.Body) == 0 {
				l.cache.Delete(key)
			} else {
				l.cache.Set(key, msg.Body, ttl)
			}
			logger.With(map[string]any{
				"action":  "lru.Subscribe",
				"id":      key,
				"referer": instanceID,
				"params":  strconv.Itoa(len(msg.Body)),
			}).Debug("cached")
			return nil
		}, true)
	if err == nil {
		logger.With(map[string]any{
			"action":  "lru.Subscribe",
			"referer": l.instanceID,
		}).Debug("init")
	}
	return err
}

// Set with ttl
func (l *Lru) Set(ctx context.Context, key string, data any, ttl time.Duration) error {
	var bytesData []byte
	if data == nil || ttl == 0 {
		l.cache.Delete(key)
	} else {
		var err error
		bytesData, err = json.Marshal(data)
		if err != nil {
			return err
		}
		l.cache.Set(key, bytesData, ttl)
	}
	if l.disableMQ {
		return nil
	}
	err := l.broker.Publish(ctx, l.topic, &broker.Message{
		Header: map[string]string{
			"id":       key,
			"instance": l.instanceID,
			"time:":    time.Now().Format(time.RFC3339),
			"ttl":      ttl.String(),
		},
		Body: bytesData,
	})
	logger := log.Extract(ctx)
	if err == nil {
		logger.With(map[string]any{
			"action":  "lru.Subscribe",
			"referer": l.instanceID,
			"id":      key,
		}).Debug("send")
	}
	return err
}

// Get the data
func (l *Lru) Get(_ context.Context, key string, data any) error {
	bytesData := l.cache.Get(key)
	if bytesData == nil {
		return status.Error(codes.NotFound, "cache not found")
	}
	if bytesData.IsExpired() {
		return status.Error(codes.NotFound, "expired")
	}
	err := json.Unmarshal(bytesData.Value(), &data)
	if err != nil {
		err = status.Errorf(codes.Internal, "cache unmarshal error %v ", err)
	}
	return err
}

// GetOrNew get exist value or new data to cache
func (l *Lru) GetOrNew(ctx context.Context, key string, data any, ttl time.Duration,
	newFn func(ctx context.Context) (any, error),
) error {
	bytesData := l.cache.Get(key)
	if bytesData != nil && !bytesData.IsExpired() {
		err := json.Unmarshal(bytesData.Value(), &data)
		if err != nil {
			err = status.Errorf(codes.Internal, "cache unmarshal error %v ", err)
		}
		return err
	}
	newData, err := newFn(ctx)
	if err != nil {
		return err
	}
	err = l.Set(ctx, key, newData, ttl)
	if err != nil {
		return err
	}
	reflect.ValueOf(data).Elem().Set(reflect.ValueOf(newData).Elem())
	return nil
}
