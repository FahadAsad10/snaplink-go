package store

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// Redis stores each link as a hash at "link:<code>". Expiring links use a
// native Redis TTL, so they disappear without a cleanup job.
type Redis struct {
	rdb *redis.Client
}

func NewRedis(rdb *redis.Client) *Redis {
	return &Redis{rdb: rdb}
}

func key(code string) string { return "link:" + code }

// createScript sets the hash only if the key is absent, plus an optional TTL, in one atomic step.
var createScript = redis.NewScript(`
if redis.call("EXISTS", KEYS[1]) == 1 then return 0 end
redis.call("HSET", KEYS[1], "url", ARGV[1], "clicks", 0, "created_at", ARGV[2], "expires_at", ARGV[3])
if tonumber(ARGV[4]) > 0 then redis.call("PEXPIRE", KEYS[1], ARGV[4]) end
return 1
`)

func (r *Redis) Create(ctx context.Context, link Link) error {
	var expires string
	var ttl int64
	if link.ExpiresAt != nil {
		expires = link.ExpiresAt.UTC().Format(time.RFC3339Nano)
		ttl = time.Until(*link.ExpiresAt).Milliseconds()
		if ttl <= 0 {
			ttl = 1
		}
	}
	ok, err := createScript.Run(ctx, r.rdb, []string{key(link.Code)},
		link.URL, link.CreatedAt.UTC().Format(time.RFC3339Nano), expires, ttl).Int()
	if err != nil {
		return err
	}
	if ok == 0 {
		return ErrExists
	}
	return nil
}

func (r *Redis) Get(ctx context.Context, code string) (Link, error) {
	vals, err := r.rdb.HGetAll(ctx, key(code)).Result()
	if err != nil {
		return Link{}, err
	}
	if len(vals) == 0 {
		return Link{}, ErrNotFound
	}
	link := Link{Code: code, URL: vals["url"]}
	link.Clicks, _ = strconv.ParseInt(vals["clicks"], 10, 64)
	link.CreatedAt, _ = time.Parse(time.RFC3339Nano, vals["created_at"])
	if t, err := time.Parse(time.RFC3339Nano, vals["expires_at"]); err == nil {
		link.ExpiresAt = &t
	}
	return link, nil
}

// hitScript increments the counter only for an existing link, so a miss
// never creates an orphan hash.
var hitScript = redis.NewScript(`
if redis.call("EXISTS", KEYS[1]) == 0 then return false end
redis.call("HINCRBY", KEYS[1], "clicks", 1)
return redis.call("HGET", KEYS[1], "url")
`)

func (r *Redis) Hit(ctx context.Context, code string) (string, error) {
	url, err := hitScript.Run(ctx, r.rdb, []string{key(code)}).Text()
	if errors.Is(err, redis.Nil) {
		return "", ErrNotFound
	}
	return url, err
}

func (r *Redis) Delete(ctx context.Context, code string) error {
	n, err := r.rdb.Del(ctx, key(code)).Result()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Redis) Ping(ctx context.Context) error {
	return r.rdb.Ping(ctx).Err()
}
