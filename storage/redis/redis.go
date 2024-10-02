package rds

import (
	"context"
	"encoding/json"
	"github.com/gomodule/redigo/redis"
	"time"
)

var cache *redis.Pool

func Init(ctx context.Context, uri string) error {
	pool := &redis.Pool{
		MaxIdle:     100,
		MaxActive:   200,
		IdleTimeout: time.Duration(10) * time.Second,
		Wait:        false,
		TestOnBorrow: func(c redis.Conn, lastUsed time.Time) error {
			_, err := c.Do("PING")
			return err
		},
		Dial: func() (redis.Conn, error) {
			c, err := redis.Dial("tcp", uri,
				redis.DialConnectTimeout(2*time.Second),
				redis.DialReadTimeout(30*time.Second),
				redis.DialWriteTimeout(2*time.Second))
			if err != nil {
				return nil, err
			}

			if _, err := c.Do("SELECT", 0); err != nil {
				c.Close()
				return nil, err
			}
			return c, nil
		},
	}
	cache = pool
	return nil
}

func Get() redis.Conn {
	return cache.Get()
}

func Stats() redis.PoolStats {
	return cache.Stats()
}

func SetString(ctx context.Context, name string, value string) error {
	conn := Get()
	defer conn.Close()
	if _, err := conn.Do("SET", name, value); err != nil {
		return err
	}
	return nil
}

func SetInt(ctx context.Context, name string, value int, expiration time.Duration) error {
	conn := Get()
	defer conn.Close()
	if _, err := conn.Do("SET", name, value, expiration); err != nil {
		return err
	}

	return nil
}

func SetStruct(ctx context.Context, name string, value interface{}) error {
	conn := Get()
	defer conn.Close()
	jsonBytes, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if _, err := conn.Do("SET", name, string(jsonBytes)); err != nil {
		return err
	}
	return nil
}

func GetInt(ctx context.Context, name string) (int, error) {
	conn := Get()
	defer conn.Close()
	if value, err := redis.Int(conn.Do("GET", name)); err == nil {
		return value, nil
	} else {
		return 0, err
	}
}

func GetString(ctx context.Context, name string) (string, error) {
	conn := Get()
	defer conn.Close()
	if value, err := redis.String(conn.Do("GET", name)); err == nil {
		return value, nil
	} else {
		return "", err
	}
}

func GetStruct(ctx context.Context, name string, v interface{}) error {
	conn := Get()
	defer conn.Close()
	if value, err := redis.String(conn.Do("GET", name)); err == nil {
		if err = json.Unmarshal([]byte(value), v); err != nil {
			return err
		} else {
			return nil
		}
	} else {
		return err
	}
}
