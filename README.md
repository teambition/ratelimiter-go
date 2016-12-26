ratelimiter-go
==========
The fastest abstract rate limiter, base on memory or redis storage.

[![Build Status](http://img.shields.io/travis/teambition/ratelimiter-go.svg?style=flat-square)](https://travis-ci.org/teambition/ratelimiter-go)
[![Coverage Status](http://img.shields.io/coveralls/teambition/ratelimiter-go.svg?style=flat-square)](https://coveralls.io/r/teambition/ratelimiter-go)
[![License](http://img.shields.io/badge/license-mit-blue.svg?style=flat-square)](https://raw.githubusercontent.com/teambition/ratelimiter-go/master/LICENSE)
[![GoDoc](http://img.shields.io/badge/go-documentation-blue.svg?style=flat-square)](http://godoc.org/github.com/teambition/ratelimiter-go)

## Features

- Distributed
- Atomicity
- High-performance
- Support redis cluster
- Support memory storage

## Installation

```sh
go get github.com/teambition/ratelimiter-go
```

## HTTP Server Demo
Try into `github.com/teambition/ratelimiter-go` directory:

```sh
go run example/main.go
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
	redis "gopkg.in/redis.v5"
)

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

func main() {
	// use memory
	// limiter := ratelimiter.New(ratelimiter.Options{
	// 	Max:      10,
	// 	Duration: time.Minute, // limit to 1000 requests in 1 minute.
	// })

	// or use redis
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})
	limiter := ratelimiter.New(ratelimiter.Options{
		Max:      10,
		Duration: time.Minute, // limit to 1000 requests in 1 minute.
		Client:   &redisClient{client},
	})

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

## Node.js version: [thunk-ratelimiter](https://github.com/thunks/thunk-ratelimiter)

## Documentation
The docs can be found at [godoc.org](https://godoc.org/github.com/teambition/ratelimiter-go), as usual.

## License
`ratelimiter-go` is licensed under the [MIT](https://github.com/teambition/ratelimiter-go/blob/master/LICENSE) license.
Copyright &copy; 2016 [Teambition](https://www.teambition.com).
