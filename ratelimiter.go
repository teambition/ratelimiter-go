package ratelimiter

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// RedisClient defines a redis client struct that ratelimiter need.
// Examples: https://github.com/teambition/ratelimiter-go/blob/master/ratelimiter_test.go#L18
/*
Implements RedisClient for a simple redis client:

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

Implements RedisClient for a cluster redis client:

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

Uses it:

	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})
	res, err := ratelimiter.New(redisClient{client}, ratelimiter.Options{})
*/
type RedisClient interface {
	RateDel(string) error
	RateEvalSha(string, []string, ...interface{}) (interface{}, error)
	RateScriptLoad(string) (string, error)
}

// Limiter struct.
type Limiter struct {
	sha1, prefix, max, duration string
	rc                          RedisClient
}

// Options for Limiter
type Options struct {
	Max      int           // The max count in duration, default is 100.
	Duration time.Duration // Count duration, default is 1 Minute.
	Prefix   string        // Redis key prefix, default is "LIMIT:".
}

// Result of limiter
type Result struct {
	Total     int           // It Equals Options.Max
	Remaining int           // It will always >= -1
	Duration  time.Duration // It Equals Options.Duration
	Reset     time.Time     // The limit recode reset time
}

// New create a limiter with a redis client and options
/*
Create a limiter with redis cluster:

	client := redis.NewClusterClient(redis.ClusterOptions{Addrs: []string{
		"localhost:7000",
		"localhost:7001",
		"localhost:7002",
		"localhost:7003",
		"localhost:7004",
		"localhost:7005",
	}})

	limiter, err := ratelimiter.New(&clusterClient{client}, limiterOptions)
*/
func New(c RedisClient, opts Options) (*Limiter, error) {
	var limiter *Limiter

	sha1, err := c.RateScriptLoad(lua)
	if err != nil {
		return limiter, err
	}

	prefix := opts.Prefix
	if prefix == "" {
		prefix = "LIMIT:"
	}
	max := "100"
	if opts.Max > 0 {
		max = strconv.FormatInt(int64(opts.Max), 10)
	}
	duration := "60000"
	if opts.Duration > 0 {
		duration = strconv.FormatInt(int64(opts.Duration/time.Millisecond), 10)
	}
	limiter = &Limiter{rc: c, sha1: sha1, prefix: prefix, max: max, duration: duration}
	return limiter, nil
}

// Get get a limiter result for id. support custom limiter policy.
/*
Get get a limiter result:

	userID := "user-123456"
	res, err := limiter.Get(userID)
	if err == nil {
		fmt.Println(res.Reset)     // 2016-10-11 21:17:53.362 +0800 CST
		fmt.Println(res.Total)     // 100
		fmt.Println(res.Remaining) // 100
		fmt.Println(res.Duration)  // 1m
	}

Get get a limiter result with custom limiter policy:

	id := "id-123456"
	policy := []int{100, 60000, 50, 60000, 50, 120000}
	res, err := limiter.Get(id, policy...)
*/
func (l *Limiter) Get(id string, policy ...int) (Result, error) {
	var result Result
	keys := []string{l.prefix + id}

	length := len(policy)
	if odd := length % 2; odd == 1 {
		return result, errors.New("ratelimiter: must be paired values")
	}

	capacity := 3
	if length > 2 {
		capacity = length + 1
	}

	args := make([]interface{}, capacity, capacity)
	args[0] = genTimestamp()
	if length == 0 {
		args[1] = l.max
		args[2] = l.duration
	} else {
		for i, val := range policy {
			if val <= 0 {
				return result, errors.New("ratelimiter: must be positive integer")
			}
			args[i+1] = strconv.FormatInt(int64(val), 10)
		}
	}

	res, err := l.getLimit(keys[0:1], args...)
	if err != nil {
		return result, err
	}

	arr := reflect.ValueOf(res)
	timestamp := arr.Index(3).Interface().(int64)
	sec := timestamp / 1000
	nsec := (timestamp - (sec * 1000)) * 1e6
	result = Result{
		Remaining: int(arr.Index(0).Interface().(int64)),
		Total:     int(arr.Index(1).Interface().(int64)),
		Duration:  time.Duration(arr.Index(2).Interface().(int64) * 1e6),
		Reset:     time.Unix(sec, nsec),
	}
	return result, nil
}

// Remove remove limiter record for id
func (l *Limiter) Remove(id string) error {
	return l.rc.RateDel(l.prefix + id)
}

func (l *Limiter) getLimit(keys []string, args ...interface{}) (res interface{}, err error) {
	res, err = l.rc.RateEvalSha(l.sha1, keys, args...)
	if err != nil && isNoScriptErr(err) {
		// try to load lua for cluster client and ring client for nodes changing.
		_, err = l.rc.RateScriptLoad(lua)
		if err == nil {
			res, err = l.rc.RateEvalSha(l.sha1, keys, args...)
		}
	}
	return
}

func genTimestamp() string {
	time := time.Now().UnixNano() / 1e6
	return strconv.FormatInt(time, 10)
}

func isNoScriptErr(err error) bool {
	var no bool
	s := err.Error()
	if strings.HasPrefix(s, "NOSCRIPT ") {
		no = true
	}
	return no
}

// copy from ./ratelimiter.lua
const lua string = `
local res = {}
local policyCount = (#ARGV - 1) / 2
local statusKey = '{' .. KEYS[1] .. '}:S'
local limit = redis.call('hmget', KEYS[1], 'ct', 'lt', 'dn', 'rt')

if limit[1] then

  res[1] = tonumber(limit[1]) - 1
  res[2] = tonumber(limit[2])
  res[3] = tonumber(limit[3]) or ARGV[3]
  res[4] = tonumber(limit[4])

  if res[1] >= 0 then
    redis.call('hincrby', KEYS[1], 'ct', -1)
  else
    res[1] = -1
  end

  if policyCount > 1 and res[1] == -1 then
    redis.call('incr', statusKey)
    redis.call('pexpire', statusKey, res[3] * 2)
  end

else

  local index = 1
  if policyCount > 1 then
    index = tonumber(redis.call('get', statusKey)) or 1
    if index > policyCount then
      index = policyCount
    end
  end

  local total = tonumber(ARGV[index * 2])
  res[1] = total - 1
  res[2] = total
  res[3] = tonumber(ARGV[index * 2 + 1])
  res[4] = tonumber(ARGV[1]) + res[3]

  redis.call('hmset', KEYS[1], 'ct', res[1], 'lt', res[2], 'dn', res[3], 'rt', res[4])
  redis.call('pexpire', KEYS[1], res[3])

  if policyCount > 1 then
    redis.call('set', statusKey, index)
    redis.call('pexpire', statusKey, res[3] * 2)
  end

end

return res
`
