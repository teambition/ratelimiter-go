package ratelimiter

import (
	"errors"
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
    limiter := ratelimiter.New(ratelimiter.Options{Client: redisClient{client}})
*/
type RedisClient interface {
	RateDel(string) error
	RateEvalSha(string, []string, ...interface{}) (interface{}, error)
	RateScriptLoad(string) (string, error)
}

// Limiter struct.
type Limiter struct {
	abstractLimiter
	prefix string
}

// Options for Limiter
type Options struct {
	Max      int           // The max count in duration for no policy, default is 100.
	Duration time.Duration // Count duration for no policy, default is 1 Minute.
	Prefix   string        // Redis key prefix, default is "LIMIT:".
	Client   RedisClient   // Use a redis client for limiter, if omit, it will use a memory limiter.
}

// Result of limiter.Get
type Result struct {
	Total     int           // It Equals Options.Max, or policy max
	Remaining int           // It will always >= -1
	Duration  time.Duration // It Equals Options.Duration, or policy duration
	Reset     time.Time     // The limit record reset time
}

// New returns a Limiter instance with given options.
// If options.Client omit, the limiter is a memory limiter
func New(opts Options) *Limiter {
	if opts.Prefix == "" {
		opts.Prefix = "LIMIT:"
	}
	if opts.Max <= 0 {
		opts.Max = 100
	}
	if opts.Duration <= 0 {
		opts.Duration = time.Minute
	}
	if opts.Client == nil {
		return newMemoryLimiter(&opts)
	}
	return newRedisLimiter(&opts)
}

type abstractLimiter interface {
	getLimit(key string, policy ...int) ([]interface{}, error)
	removeLimit(key string) error
}

func newRedisLimiter(opts *Options) *Limiter {
	sha1, err := opts.Client.RateScriptLoad(lua)
	if err != nil {
		panic(err)
	}
	r := &redisLimiter{
		rc:       opts.Client,
		sha1:     sha1,
		max:      strconv.FormatInt(int64(opts.Max), 10),
		duration: strconv.FormatInt(int64(opts.Duration/time.Millisecond), 10),
	}
	return &Limiter{r, opts.Prefix}
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
	key := l.prefix + id

	if odd := len(policy) % 2; odd == 1 {
		return result, errors.New("ratelimiter: must be paired values")
	}

	res, err := l.getLimit(key, policy...)
	if err != nil {
		return result, err
	}

	result = Result{}
	switch res[3].(type) {
	case time.Time: // result from memory limiter
		result.Remaining = res[0].(int)
		result.Total = res[1].(int)
		result.Duration = res[2].(time.Duration)
		result.Reset = res[3].(time.Time)
	default: // result from redis limiter
		result.Remaining = int(res[0].(int64))
		result.Total = int(res[1].(int64))
		result.Duration = time.Duration(res[2].(int64) * 1e6)

		timestamp := res[3].(int64)
		sec := timestamp / 1000
		result.Reset = time.Unix(sec, (timestamp-(sec*1000))*1e6)
	}
	return result, nil
}

// Remove remove limiter record for id
func (l *Limiter) Remove(id string) error {
	return l.removeLimit(l.prefix + id)
}

type redisLimiter struct {
	sha1, max, duration string
	rc                  RedisClient
}

func (r *redisLimiter) removeLimit(key string) error {
	return r.rc.RateDel(key)
}

func (r *redisLimiter) getLimit(key string, policy ...int) ([]interface{}, error) {
	keys := []string{key}
	capacity := 3
	length := len(policy)
	if length > 2 {
		capacity = length + 1
	}

	args := make([]interface{}, capacity, capacity)
	args[0] = genTimestamp()
	if length == 0 {
		args[1] = r.max
		args[2] = r.duration
	} else {
		for i, val := range policy {
			if val <= 0 {
				return nil, errors.New("ratelimiter: must be positive integer")
			}
			args[i+1] = strconv.FormatInt(int64(val), 10)
		}
	}

	res, err := r.rc.RateEvalSha(r.sha1, keys, args...)
	if err != nil && isNoScriptErr(err) {
		// try to load lua for cluster client and ring client for nodes changing.
		_, err = r.rc.RateScriptLoad(lua)
		if err == nil {
			res, err = r.rc.RateEvalSha(r.sha1, keys, args...)
		}
	}

	if err == nil {
		arr, ok := res.([]interface{})
		if ok && len(arr) == 4 {
			return arr, nil
		}
		err = errors.New("Invalid result")
	}
	return nil, err
}

func genTimestamp() string {
	time := time.Now().UnixNano() / 1e6
	return strconv.FormatInt(time, 10)
}

func isNoScriptErr(err error) bool {
	return strings.HasPrefix(err.Error(), "NOSCRIPT ")
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
  
  if policyCount > 1 and res[1] == -1 then
    redis.call('incr', statusKey)
    redis.call('pexpire', statusKey, res[3] * 2)
    local index = tonumber(redis.call('get', statusKey))
    if index == 1 then
      redis.call('incr', statusKey)
    end
  end
  
  if res[1] >= -1 then
    redis.call('hincrby', KEYS[1], 'ct', -1)
  else
    res[1] = -1
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

end

return res
`
