package ratelimiter

import (
	"errors"
	"reflect"
	"strconv"
	"time"

	redis "gopkg.in/redis.v5"
)

// Limiter struct.
type Limiter struct {
	sha1, prefix, max, duration string
	redis                       *redis.Client
}

// Options for Limiter
type Options struct {
	Max      int           // Default is 100.
	Duration time.Duration // Default is 1 Minute.
	Prefix   string        // Default is "LIMIT".
}

// Result of limiter
type Result struct {
	Total, Remaining int
	Duration         time.Duration
	Reset            time.Time
}

// New create a limiter with options
func New(c *redis.Client, opts Options) (*Limiter, error) {
	var limiter *Limiter

	sha1, err := c.ScriptLoad(lua).Result()
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
	limiter = &Limiter{redis: c, sha1: sha1, prefix: prefix, max: max, duration: duration}
	return limiter, nil
}

// Get get a limiter result for id. support custom limiter policy.
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

	res, err := l.redis.EvalSha(l.sha1, keys[0:1], args...).Result()
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

// Remove limiter record for id
func (l *Limiter) Remove(id string) (int, error) {
	var num int
	res, err := l.redis.Del(l.prefix + id).Result()
	if err == nil {
		num = int(reflect.ValueOf(res).Interface().(int64))
	}
	return num, err
}

func genTimestamp() string {
	time := time.Now().UnixNano() / 1e6
	return strconv.FormatInt(time, 10)
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

  if policyCount > 1 and res[1] == 0 then
    redis.call('incr', statusKey)
    redis.call('pexpire', statusKey, res[3] * 2)
  end

  if res[1] >= 0 then
    redis.call('hincrby', KEYS[1], 'ct', -1)
  else
    res[1] = 0
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
