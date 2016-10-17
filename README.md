ratelimiter-go
==========
The fastest abstract rate limiter, base on go-redis/redis.

[![Build Status][travis-image]][travis-url]

Summary
-------
- [Requirements](#requirements)
- [Features](#features)
- [Installation](#installation)
- [HTTP Server Demo](#http-server-demo)
- [API](#api)
	- [type Limiter](#type-limiter)
		- [func New](#func-new)
		- [func (*Limiter) Get](#func-limiter-get)
		- [func (*Limiter) Remove](#func-limiter-remove)
	- [type Options](#type-options)
	- [type RedisClient](#type-redisclient)
	- [type Result](#type-result)
- [License MIT](#license)

## Requirements

- Redis 3+

## Features

- Distributed
- Atomicity
- High-performance
- Support redis cluster

## Installation

```sh
glide get github.com/teambition/ratelimiter-go
```

or
```sh
go get github.com/teambition/ratelimiter-go
```

## HTTP Server Demo

Try in `github.com/teambition/ratelimiter-go` directory:
```sh
go run ratelimiter/main.go
```

Visit: http://127.0.0.1:8080/

```go
package main

import (
	"fmt"
	"html"
	"log"
	"net/http"
	"strconv"
	"time"

	ratelimiter "github.com/teambition/ratelimiter-go"
	redis "gopkg.in/redis.v4"
)

var limiter *ratelimiter.Limiter

// Implements RedisClient for redis.Client
type redisClient struct {
	*redis.Client
}

func (c *redisClient) RateDel(key string) error {
	return c.Del(key).Err()
}
func (c *redisClient) RateEvalSha(sha1 string, keys []string, args ...interface{}) (interface{}, error) {
	return c.EvalSha(sha1, keys, args...).Result()
}
func (c *redisClient) RateScriptLoad(script string) (string, error) {
	return c.ScriptLoad(script).Result()
}

func init() {
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	var err error
	limiter, err = ratelimiter.New(&redisClient{client}, ratelimiter.Options{
		Max:      10,
		Duration: time.Minute, // limit to 1000 requests in 1 minute.
	})
	if err != nil {
		panic(err)
	}
}

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		res, err := limiter.Get(r.URL.Path)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}

		header := w.Header()
		header.Set("X-Ratelimit-Limit", strconv.FormatInt(int64(res.Total), 10))
		header.Set("X-Ratelimit-Remaining", strconv.FormatInt(int64(res.Remaining), 10))
		header.Set("X-Ratelimit-Reset", strconv.FormatInt(res.Reset.Unix(), 10))

		if res.Remaining >= 0 {
			w.WriteHeader(200)
			fmt.Fprintf(w, "Path: %q\n", html.EscapeString(r.URL.Path))
			fmt.Fprintf(w, "Remaining: %d\n", res.Remaining)
			fmt.Fprintf(w, "Total: %d\n", res.Total)
			fmt.Fprintf(w, "Duration: %v\n", res.Duration)
			fmt.Fprintf(w, "Reset: %v\n", res.Reset)
		} else {
			after := int64(res.Reset.Sub(time.Now())) / 1e9
			header.Set("Retry-After", strconv.FormatInt(after, 10))
			w.WriteHeader(429)
			fmt.Fprintf(w, "Rate limit exceeded, retry in %d seconds.\n", after)
		}
	})
	log.Fatal(http.ListenAndServe(":8080", nil))
}
```

## API
Package ratelimiter provides the fastest abstract rate limiter, base on go-redis/redis.
```go
  import "github.com/teambition/ratelimiter-go"
```

### type Limiter
Limiter struct.
```go
type Limiter struct {
    // contains filtered or unexported fields
}
```

#### func New
New create a limiter with a redis client and options.
```go
func New(c RedisClient, opts Options) (*Limiter, error)
```

Create a limiter with redis cluster:
```go
client := redis.NewClusterClient(redis.ClusterOptions{Addrs: []string{
	"localhost:7000",
	"localhost:7001",
	"localhost:7002",
	"localhost:7003",
	"localhost:7004",
	"localhost:7005",
}})

limiter, err := ratelimiter.New(&clusterClient{client}, limiterOptions)
```

#### func (*Limiter) Get
Get get a limiter result for id. support custom limiter policy.
```go
func (l *Limiter) Get(id string, policy ...int) (Result, error)
```

Get get a limiter result:
```go
userID := "user-123456"
res, err := limiter.Get(userID)
if err == nil {
	fmt.Println(res.Reset)     // 2016-10-11 21:17:53.362 +0800 CST
	fmt.Println(res.Total)     // 100
	fmt.Println(res.Remaining) // 100
	fmt.Println(res.Duration)  // 1m
}
```

Get get a limiter result with custom limiter policy:
```go
id := "id-123456"
policy := []int{100, 60000, 50, 60000, 50, 120000}
res, err := limiter.Get(id, policy...)
```

#### func (*Limiter) Remove
Remove remove limiter record for id
```go
func (l *Limiter) Remove(id string) error
```

### type Options
Options for Limiter
```go
type Options struct {
    Max      int           // The max count in duration, default is 100.
    Duration time.Duration // Count duration, default is 1 Minute.
    Prefix   string        // Redis key prefix, default is "LIMIT:".
}
```

### type RedisClient
RedisClient defines a redis client struct that ratelimiter need. Examples: https://github.com/teambition/ratelimiter-go/blob/master/ratelimiter_test.go#L18
```go
type RedisClient interface {
    RateDel(string) error
    RateEvalSha(string, []string, ...interface{}) (interface{}, error)
    RateScriptLoad(string) (string, error)
}
```

Implements `RedisClient` for a simple redis client:
```go
import "gopkg.in/redis.v4"

type redisClient struct {
	*redis.Client
}

func (c *redisClient) RateDel(key string) error {
	return c.Del(key).Err()
}
func (c *redisClient) RateEvalSha(sha1 string, keys []string, args ...interface{}) (interface{}, error) {
	return c.EvalSha(sha1, keys, args...).Result()
}
func (c *redisClient) RateScriptLoad(script string) (string, error) {
	return c.ScriptLoad(lua).Result()
}
```

Implements `RedisClient` for a cluster redis client:
```go
import "gopkg.in/redis.v4"

type clusterClient struct {
	*redis.ClusterClient
}

func (c *clusterClient) RateDel(key string) error {
	return c.Del(key).Err()
}
func (c *clusterClient) RateEvalSha(sha1 string, keys []string, args ...interface{}) (interface{}, error) {
	return c.EvalSha(sha1, keys, args...).Result()
}
func (c *clusterClient) RateScriptLoad(script string) (string, error) {
	var sha1 string
	err := c.ForEachMaster(func(client *redis.Client) error {
		res, err := client.ScriptLoad(script).Result()
		if err == nil {
			sha1 = res
		}
		return err
	})
	return sha1, err
}
```

Uses it:
```go
client := redis.NewClient(&redis.Options{
	Addr: "localhost:6379",
})
res, err := ratelimiter.New(redisClient{client}, ratelimiter.Options{})
```

### type Result
Result of limiter
```go
type Result struct {
    Total     int           // It Equals Options.Max
    Remaining int           // It will always >= -1
    Duration  time.Duration // It Equals Options.Duration
    Reset     time.Time     // The limit recode reset time
}
```

## Node.js version: [thunk-ratelimiter](https://github.com/thunks/thunk-ratelimiter)

## License
`ratelimiter-go` is licensed under the [MIT](https://github.com/teambition/ratelimiter-go/blob/master/LICENSE) license.
Copyright &copy; 2016 [Teambition](https://www.teambition.com).

[travis-url]: https://travis-ci.org/teambition/ratelimiter-go
[travis-image]: http://img.shields.io/travis/teambition/ratelimiter-go.svg
