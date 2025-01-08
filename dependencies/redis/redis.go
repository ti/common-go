// Package redis implements dependency of rueidis.
package redis

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ti/common-go/dependencies/uri"

	"github.com/redis/rueidis/rueidiscompat"

	"github.com/redis/rueidis"
	"github.com/redis/rueidis/rueidislock"
)

// Redis instance
type Redis struct {
	client      rueidis.Client
	locker      rueidislock.Locker
	rateLimiter *rateLimiter
	cmdable     rueidiscompat.Cmdable
}

// New redis instance
func New(ctx context.Context, uri string) (*Redis, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return nil, err
	}
	cli := &Redis{}
	err = cli.Init(ctx, u)
	return cli, err
}

// Init initialization
func (r *Redis) Init(ctx context.Context, u *url.URL) error {
	hosts := strings.Split(u.Host, ",")
	query := u.Query()
	opts := rueidis.ClientOption{
		InitAddress: hosts,
		ClientName:  query.Get("client"),
	}
	if u.User != nil {
		opts.Username = u.User.Username()
		opts.Password, _ = u.User.Password()
	}
	const valueTrue = "true"
	opts.ShuffleInit = query.Get("shuffle") == valueTrue
	masterSet := query.Get("master")
	opts.DisableCache = true
	if query.Get("cache") == valueTrue {
		opts.DisableCache = false
	}
	if masterSet != "" {
		opts.Sentinel = rueidis.SentinelOption{
			MasterSet:  masterSet,
			Username:   opts.Username,
			Password:   opts.Password,
			ClientName: opts.ClientName,
		}
	}
	opts.SelectDB, _ = strconv.Atoi(query.Get("db"))
	client, err := rueidis.NewClient(opts)
	if err != nil {
		return err
	}
	err = client.Do(ctx, client.B().Ping().Build()).Error()
	if err != nil {
		return errors.New("redis can not dial " + u.String() + " - " + err.Error())
	}
	r.client = client
	var lockerOpts rueidislock.LockerOption
	err = uri.Unmarshal(u, &lockerOpts)
	if err != nil {
		return fmt.Errorf("unmarshal to redis locker error %w", err)
	}
	lockerOpts.ClientOption = opts
	r.locker, err = rueidislock.NewLocker(lockerOpts)
	if err != nil {
		return errors.New("redis locker error " + u.String() + " - " + err.Error())
	}
	r.rateLimiter = newRateLimiter(r.client, time.Minute, 10*time.Second)
	r.cmdable = rueidiscompat.NewAdapter(r.client)
	return nil
}

// Close redis
func (r *Redis) Close(_ context.Context) error {
	r.locker.Close()
	r.client.Close()
	return nil
}

// Locker new locker from redis
func (r *Redis) Locker() rueidislock.Locker {
	return r.locker
}

// Client redis client
func (r *Redis) Client() rueidis.Client {
	return r.client
}

// Cmdable the redis cmdable
func (r *Redis) Cmdable() rueidiscompat.Cmdable {
	return r.cmdable
}
