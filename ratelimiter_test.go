package ratelimiter_test

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/teambition/ratelimiter-go"
	"gopkg.in/redis.v5"
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

// Implements RedisClient for redis.ClusterClient
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

// Implements RedisClient for redis.Ring
type ringClient struct {
	*redis.Ring
}

func (c *ringClient) RateDel(key string) error {
	return c.Del(key).Err()
}
func (c *ringClient) RateEvalSha(sha1 string, keys []string, args ...interface{}) (interface{}, error) {
	return c.EvalSha(sha1, keys, args...).Result()
}
func (c *ringClient) RateScriptLoad(script string) (string, error) {
	var sha1 string
	err := c.ForEachShard(func(client *redis.Client) error {
		res, err := client.ScriptLoad(script).Result()
		if err == nil {
			sha1 = res
		}
		return err
	})
	return sha1, err
}

var client = redis.NewClient(&redis.Options{
	Addr: "localhost:6379",
})
var limiter *ratelimiter.Limiter

func TestMain(m *testing.M) {

	code := m.Run()
	client.Close()
	os.Exit(code)
}

func TestRedis(t *testing.T) {
	assert := assert.New(t)

	pong, err := client.Ping().Result()
	assert.Nil(err)
	assert.Equal("PONG", pong)
}
func TestRatelimiterGo(t *testing.T) {
	assert := assert.New(t)
	t.Run("ratelimiter.New, With default options", func(t *testing.T) {

		var limiter ratelimiter.AbstractLimiter
		var id = genID()
		t.Run("ratelimiter.New", func(t *testing.T) {
			res, err := ratelimiter.New(ratelimiter.Options{Client: &redisClient{client}})
			assert.Nil(err)
			limiter = res
		})

		t.Run("limiter.Get", func(t *testing.T) {
			res, err := limiter.Get(id)
			assert.Nil(err)
			assert.Equal(res.Total, 100)
			assert.Equal(res.Remaining, 99)
			assert.Equal(res.Duration, time.Duration(60*1e9))
			assert.True(res.Reset.UnixNano() > time.Now().UnixNano())

			res, err = limiter.Get(id)
			assert.Nil(err)
			assert.Equal(res.Total, 100)
			assert.Equal(res.Remaining, 98)
		})
		t.Run("limiter.Remove", func(t *testing.T) {
			err := limiter.Remove(id)
			assert.Nil(err)

			err = limiter.Remove(id)
			assert.Nil(err)

			res, err := limiter.Get(id)
			assert.Nil(err)
			assert.Equal(res.Total, 100)
			assert.Equal(res.Remaining, 99)
		})
		t.Run("limiter.Get with invalid args", func(t *testing.T) {
			_, err := limiter.Get(id, 10)
			assert.Equal("ratelimiter: must be paired values", err.Error())

			_, err2 := limiter.Get(id, -1, 10)
			assert.Equal("ratelimiter: must be positive integer", err2.Error())

			_, err3 := limiter.Get(id, 10, 0)

			assert.Equal("ratelimiter: must be positive integer", err3.Error())
		})
	})
	t.Run("ratelimiter.New, With options", func(t *testing.T) {
		var limiter ratelimiter.AbstractLimiter
		var id = genID()
		t.Run("ratelimiter.New", func(t *testing.T) {
			res, err := ratelimiter.New(ratelimiter.Options{
				Client:   &redisClient{client},
				Max:      3,
				Duration: time.Second,
			})
			assert.Nil(err)
			limiter = res
		})

		t.Run("limiter.Get", func(t *testing.T) {
			res, err := limiter.Get(id)
			assert.Nil(err)
			assert.Equal(3, res.Total)
			assert.Equal(2, res.Remaining)
			assert.Equal(time.Second, res.Duration)
			assert.True(res.Reset.UnixNano() > time.Now().UnixNano())
			assert.True(res.Reset.UnixNano() <= time.Now().Add(time.Second).UnixNano())

			res, err = limiter.Get(id)
			assert.Equal(res.Remaining, 1)
			res, err = limiter.Get(id)
			assert.Equal(res.Remaining, 0)
			res, err = limiter.Get(id)
			assert.Equal(res.Remaining, -1)
			res, err = limiter.Get(id)
			assert.Equal(res.Remaining, -1)
		})

		t.Run("limiter.Remove", func(t *testing.T) {
			err := limiter.Remove(id)
			assert.Nil(err)

			res2, err2 := limiter.Get(id)
			assert.Nil(err2)
			assert.Equal(res2.Remaining, 2)
		})

		t.Run("limiter.Get with multi-policy", func(t *testing.T) {
			id := genID()
			policy := []int{2, 100, 2, 200, 1, 300}

			res, err := limiter.Get(id, policy...)
			assert.Nil(err)
			assert.Equal(2, res.Total)
			assert.Equal(1, res.Remaining)
			assert.Equal(time.Millisecond*100, res.Duration)

			res, err = limiter.Get(id, policy...)
			assert.Nil(err)
			assert.Equal(0, res.Remaining)

			res, err = limiter.Get(id, policy...)
			assert.Nil(err)
			assert.Equal(-1, res.Remaining)

			time.Sleep(res.Duration + time.Millisecond)
			res, err = limiter.Get(id, policy...)
			assert.Nil(err)
			assert.Equal(2, res.Total)
			assert.Equal(1, res.Remaining)
			assert.Equal(time.Millisecond*200, res.Duration)

			res, err = limiter.Get(id, policy...)
			assert.Nil(err)
			assert.Equal(0, res.Remaining)

			res, err = limiter.Get(id, policy...)
			assert.Nil(err)
			assert.Equal(-1, res.Remaining)

			time.Sleep(res.Duration + time.Millisecond)
			res, err = limiter.Get(id, policy...)
			assert.Nil(err)
			assert.Equal(1, res.Total)
			assert.Equal(0, res.Remaining)
			assert.Equal(time.Millisecond*300, res.Duration)

			res, err = limiter.Get(id, policy...)
			assert.Nil(err)
			assert.Equal(res.Remaining, -1)

			// restore after double Duration
			time.Sleep(res.Duration*2 + time.Millisecond)
			res, err = limiter.Get(id, policy...)
			assert.Nil(err)
			assert.Equal(2, res.Total)
			assert.Equal(1, res.Remaining)
			assert.Equal(time.Millisecond*100, res.Duration)

		})
		t.Run("limiter.Get with multi-policy for expired", func(t *testing.T) {
			id := genID()
			policy := []int{2, 100, 2, 200, 1, 200, 1, 300}

			//First policy
			res, err := limiter.Get(id, policy...)
			assert.Nil(err)
			assert.Equal(2, res.Total)
			assert.Equal(1, res.Remaining)
			assert.Equal(time.Millisecond*100, res.Duration)

			res, err = limiter.Get(id, policy...)
			assert.Equal(0, res.Remaining)

			res, err = limiter.Get(id, policy...)
			assert.Equal(-1, res.Remaining)

			//Second policy
			time.Sleep(res.Duration + time.Millisecond)
			res, err = limiter.Get(id, policy...)
			assert.Equal(2, res.Total)
			assert.Equal(1, res.Remaining)
			assert.Equal(time.Millisecond*200, res.Duration)

			res, err = limiter.Get(id, policy...)
			assert.Equal(0, res.Remaining)

			res, err = limiter.Get(id, policy...)
			assert.Equal(-1, res.Remaining)

			//Third policy
			time.Sleep(res.Duration + time.Millisecond)
			res, err = limiter.Get(id, policy...)
			assert.Equal(1, res.Total)
			assert.Equal(0, res.Remaining)
			assert.Equal(time.Millisecond*200, res.Duration)

			res, err = limiter.Get(id, policy...)
			assert.Equal(-1, res.Remaining)

			// restore to First policy after Third policy*2 Duration
			time.Sleep(res.Duration*2 + time.Millisecond)
			res, err = limiter.Get(id, policy...)
			assert.Nil(err)
			assert.Equal(2, res.Total)
			assert.Equal(1, res.Remaining)
			assert.Equal(time.Millisecond*100, res.Duration)

			//Second policy
			time.Sleep(res.Duration + time.Millisecond)
			res, err = limiter.Get(id, policy...)
			assert.Equal(2, res.Total)
			assert.Equal(1, res.Remaining)

			res, err = limiter.Get(id, policy...)
			assert.Equal(0, res.Remaining)

			res, err = limiter.Get(id, policy...)
			assert.Equal(-1, res.Remaining)
			assert.Equal(res.Duration, time.Millisecond*200)

			//Third policy
			time.Sleep(res.Duration + time.Millisecond)
			res, err = limiter.Get(id, policy...)
			assert.Equal(1, res.Total)
			assert.Equal(0, res.Remaining)
			assert.Equal(time.Millisecond*200, res.Duration)

			res, err = limiter.Get(id, policy...)
			assert.Equal(-1, res.Remaining)

			//Fourth policy
			time.Sleep(res.Duration + time.Millisecond)
			res, err = limiter.Get(id, policy...)
			assert.Equal(1, res.Total)
			assert.Equal(0, res.Remaining)
			assert.Equal(res.Duration, time.Millisecond*300)

			res, err = limiter.Get(id, policy...)
			assert.Equal(1, res.Total)
			assert.Equal(-1, res.Remaining)

			res, err = limiter.Get(id, policy...)
			assert.Equal(1, res.Total)
			assert.Equal(-1, res.Remaining)

			// restore to First policy after Fourth policy*2 Duration
			time.Sleep(res.Duration*2 + time.Millisecond)
			res, err = limiter.Get(id, policy...)
			assert.Nil(err)
			assert.Equal(2, res.Total)
			assert.Equal(1, res.Remaining)
			assert.Equal(time.Millisecond*100, res.Duration)
		})
	})

	t.Run("ratelimiter.New, Chaos", func(t *testing.T) {

		t.Run("10 limiters work for one id", func(t *testing.T) {

			var wg sync.WaitGroup
			var id = genID()
			var result = NewResult(make([]int, 10000))

			var redisOptions = redis.Options{Addr: "localhost:6379"}

			var worker = func(c *redis.Client, l ratelimiter.AbstractLimiter) {
				defer wg.Done()
				defer c.Close()

				for i := 0; i < 1000; i++ {
					res, err := l.Get(id)
					assert.Nil(err)
					result.Push(res.Remaining)
				}
			}

			wg.Add(10)
			for i := 0; i < 10; i++ {
				client := redis.NewClient(&redisOptions)

				limiter, err := ratelimiter.New(ratelimiter.Options{Client: &redisClient{client}, Max: 9998})
				assert.Nil(err)
				go worker(client, limiter)
			}

			wg.Wait()
			s := result.Value()
			sort.Ints(s) // [-1 -1 0 1 2 3 ... 9997 9997]
			assert.Equal(s[0], -1)
			for i := 1; i < 10000; i++ {
				assert.Equal(s[i], i-2)
			}
		})
	})

	t.Run("ratelimiter.New with redis ring, Chaos", func(t *testing.T) {
		t.Run("10 limiters work for one id", func(t *testing.T) {
			t.Skip("Can't run in travis")

			var wg sync.WaitGroup
			var id = genID()
			var result = NewResult(make([]int, 10000))

			var redisOptions = redis.RingOptions{Addrs: map[string]string{
				"a": "localhost:6379",
				"b": "localhost:6380",
			}}

			var worker = func(c *redis.Ring, l ratelimiter.AbstractLimiter) {
				defer wg.Done()
				defer c.Close()

				for i := 0; i < 1000; i++ {
					res, err := l.Get(id)
					assert.Nil(err)
					result.Push(res.Remaining)
				}
			}

			wg.Add(10)
			for i := 0; i < 10; i++ {
				client := redis.NewRing(&redisOptions)
				limiter, err := ratelimiter.New(ratelimiter.Options{Client: &ringClient{client}, Max: 9998})
				assert.Nil(err)
				go worker(client, limiter)
			}

			wg.Wait()
			s := result.Value()
			sort.Ints(s) // [-1 -1 0 1 2 3 ... 9997 9997]
			assert.Equal(s[0], -1)
			for i := 1; i < 10000; i++ {
				assert.Equal(s[i], i-2)
			}
		})
	})

	t.Run("ratelimiter.New with redis cluster, Chaos", func(t *testing.T) {
		t.Run("10 limiters work for one id", func(t *testing.T) {
			t.Skip("Can't run in travis")

			var wg sync.WaitGroup
			var id = genID()
			var result = NewResult(make([]int, 10000))

			var redisOptions = redis.ClusterOptions{Addrs: []string{
				"localhost:7000",
				"localhost:7001",
				"localhost:7002",
				"localhost:7003",
				"localhost:7004",
				"localhost:7005",
			}}

			var worker = func(c *redis.ClusterClient, l ratelimiter.AbstractLimiter) {
				defer wg.Done()
				defer c.Close()

				for i := 0; i < 1000; i++ {
					res, err := l.Get(id)
					assert.Nil(err)
					result.Push(res.Remaining)
				}
			}

			wg.Add(10)
			for i := 0; i < 10; i++ {
				client := redis.NewClusterClient(&redisOptions)
				limiter, err := ratelimiter.New(ratelimiter.Options{Client: &clusterClient{client}, Max: 9998})
				assert.Nil(err)
				go worker(client, limiter)
			}

			wg.Wait()
			s := result.Value()
			sort.Ints(s) // [-1 -1 0 1 2 3 ... 9997 9997]
			assert.Equal(s[0], -1)
			for i := 1; i < 10000; i++ {
				assert.Equal(s[i], i-2)
			}
		})
	})
}

func genID() string {
	buf := make([]byte, 12)
	_, err := rand.Read(buf)
	if err != nil {
		panic(err)
	}
	return hex.EncodeToString(buf)
}

type Result struct {
	i   int
	len int
	s   []int
	m   sync.Mutex
}

func NewResult(s []int) Result {
	return Result{s: s, len: len(s)}
}

func (r *Result) Push(val int) {
	r.m.Lock()
	if r.i == r.len {
		panic(errors.New("Result overflow"))
	}
	r.s[r.i] = val
	r.i++
	r.m.Unlock()
}

func (r *Result) Value() []int {
	return r.s
}
