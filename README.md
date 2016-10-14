ratelimiter-go
==========
The fastest abstract rate limiter, base on go-redis/redis.

[![Build Status][travis-image]][travis-url]

## Requirements

- Redis 2.8+

## Installation

```sh
glide get github.com/teambition/ratelimiter-go
```

or
```sh
go get github.com/teambition/ratelimiter-go
```

## Try HTTP Server Demo

Try in `github.com/teambition/ratelimiter-go` directory:
```sh
go run ratelimiter/main.go
```
Visit: http://127.0.0.1:8080/

## Example

 Example Connect middleware implementation limiting against a `user._id`:

```go
func ExampleRatelimiterGo() {
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	limiter, err := ratelimiter.New(client, ratelimiter.Options{
		Max:      1000,
		Duration: time.Minute, // limit to 1000 requests in 1 minute.
	})
	if err != nil {
		panic(err)
	}

	userID := "user-12345"
	res, err := limiter.Get(userID)
	if err != nil {
		panic(err)
	}
	// fmt.Println(res.Reset) Reset time: 2016-10-11 21:17:53.362 +0800 CST
	fmt.Println(res.Total)
	fmt.Println(res.Remaining)
	fmt.Println(res.Duration)
	// Output:
	// 1000
	// 999
	// 1m0s
}
```

## PACKAGE DOCUMENTATION

### package ratelimiter

```go
  import "github.com/teambition/ratelimiter-go"
```
Package ratelimiter provides the fastest abstract rate limiter, base on go-redis/redis.

### TYPES

```go
type Limiter struct {
    // contains filtered or unexported fields
}
```
Limiter struct.

```go
func New(c *redis.Client, opts Options) (*Limiter, error)
```
New create a limiter with a redis client and options

```go
func ClusterNew(c *redis.ClusterClient, opts Options) (*Limiter, error)
```
ClusterNew create a limiter with a redis cluster client and options

```go
func RingNew(c *redis.Ring, opts Options) (*Limiter, error)
```
RingNew create a limiter with a redis ring client and options


```go
func (l *Limiter) Get(id string, policy ...int) (Result, error)
// res, err := limiter.Get(UserId1)
// res, err := limiter.Get(UserId2, 100, 60000)
// res, err := limiter.Get(UserId3, 100, 60000, 50, 60000)
```
Get get a limiter result for id. support custom limiter policy.

```go
func (l *Limiter) Remove(id string) (int, error)
// res, err := limiter.Remove(UserId1)
```
Remove limiter record for id

```go
type Options struct {
    Max      int           // Default is 100.
    Duration time.Duration // Default is 1 Minute.
    Prefix   string        // Default is "LIMIT".
}
```
Options for Limiter

```go
type Result struct {
    Total, Remaining int
    Duration         time.Duration
    Reset            time.Time
}
```
Result of limiter

## Node.js version: [thunk-ratelimiter](https://github.com/thunks/thunk-ratelimiter)


[travis-url]: https://travis-ci.org/teambition/ratelimiter-go
[travis-image]: http://img.shields.io/travis/teambition/ratelimiter-go.svg
