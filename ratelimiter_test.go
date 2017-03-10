package ratelimiter_test

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/teambition/ratelimiter-go"
	"gopkg.in/redis.v5"
)

// Implements RedisClient for redis.Client
type redisFailedClient struct {
	*redis.Client
}

func (c *redisFailedClient) RateDel(key string) error {
	return c.Del(key).Err()
}

func (c *redisFailedClient) RateEvalSha(sha1 string, keys []string, args ...interface{}) (interface{}, error) {
	return nil, errors.New("NOSCRIPT mock error")
}

func (c *redisFailedClient) RateScriptLoad(script string) (string, error) {
	return c.ScriptLoad(script).Result()
}

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

func TestRedisRatelimiter(t *testing.T) {
	var client = redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})
	pong, err := client.Ping().Result()
	assert.Nil(t, err)
	assert.Equal(t, "PONG", pong)
	defer client.Close()

	t.Run("ratelimiter.New, With default options", func(t *testing.T) {
		assert := assert.New(t)

		var limiter *ratelimiter.Limiter
		var id = genID()
		t.Run("ratelimiter.New", func(t *testing.T) {
			limiter = ratelimiter.New(ratelimiter.Options{Client: &redisClient{client}})
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
		assert := assert.New(t)

		var limiter *ratelimiter.Limiter
		var id = genID()
		t.Run("ratelimiter.New", func(t *testing.T) {
			limiter = ratelimiter.New(ratelimiter.Options{
				Client:   &redisClient{client},
				Max:      3,
				Duration: time.Second,
			})
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
			policy := []int{2, 100, 2, 200, 3, 300, 3, 400}

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
			time.Sleep(res.Duration + 5*time.Millisecond)
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
			assert.Equal(3, res.Total)
			assert.Equal(2, res.Remaining)
			assert.Equal(time.Millisecond*300, res.Duration)

			res, err = limiter.Get(id, policy...)
			res, err = limiter.Get(id, policy...)
			res, err = limiter.Get(id, policy...)
			assert.Equal(-1, res.Remaining)

			// restore to First policy after Third policy*2 Duration
			time.Sleep(res.Duration*2 + time.Millisecond)
			res, err = limiter.Get(id, policy...)
			assert.Nil(err)
			assert.Equal(2, res.Total)
			assert.Equal(1, res.Remaining)
			assert.Equal(time.Millisecond*100, res.Duration)
			res, err = limiter.Get(id, policy...)
			res, err = limiter.Get(id, policy...)
			assert.Equal(-1, res.Remaining)

			//Second policy
			time.Sleep(res.Duration + time.Millisecond)
			res, err = limiter.Get(id, policy...)
			assert.Equal(2, res.Total)
			assert.Equal(1, res.Remaining)

			res, err = limiter.Get(id, policy...)
			assert.Equal(0, res.Remaining)

			res, err = limiter.Get(id, policy...)
			assert.Equal(-1, res.Remaining)
			assert.Equal(time.Millisecond*200, res.Duration)

			//Third policy
			time.Sleep(res.Duration + time.Millisecond)
			res, err = limiter.Get(id, policy...)
			assert.Equal(3, res.Total)
			assert.Equal(2, res.Remaining)
			assert.Equal(time.Millisecond*300, res.Duration)

			res, err = limiter.Get(id, policy...)
			assert.Equal(1, res.Remaining)
			assert.Equal(time.Millisecond*300, res.Duration)

			res, err = limiter.Get(id, policy...)
			res, err = limiter.Get(id, policy...)
			assert.Equal(-1, res.Remaining)

			//Fourth policy
			time.Sleep(res.Duration + time.Millisecond)
			res, err = limiter.Get(id, policy...)
			assert.Equal(3, res.Total)
			assert.Equal(2, res.Remaining)
			assert.Equal(time.Millisecond*400, res.Duration)

			res, err = limiter.Get(id, policy...)
			assert.Equal(3, res.Total)
			assert.Equal(1, res.Remaining)

			res, err = limiter.Get(id, policy...)
			assert.Equal(3, res.Total)
			assert.Equal(0, res.Remaining)

			res, err = limiter.Get(id, policy...)
			assert.Equal(3, res.Total)
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
	t.Run("limiter.Get with multi-policy situation for expired", func(t *testing.T) {
		assert := assert.New(t)

		var id = genID()

		limiter := ratelimiter.New(ratelimiter.Options{
			Client: &redisClient{client},
		})

		policy := []int{2, 150, 2, 200, 3, 300, 3, 400}

		//用户访问数在第一个策略限制内
		res, err := limiter.Get(id, policy...)
		assert.Nil(err)
		assert.Equal(2, res.Total)
		assert.Equal(1, res.Remaining)
		assert.Equal(time.Millisecond*150, res.Duration)

		//第一个策略正常过期，第二次会继续走第一个
		time.Sleep(res.Duration + time.Millisecond)
		res, err = limiter.Get(id, policy...)
		assert.Equal(2, res.Total)
		assert.Equal(1, res.Remaining)
		assert.Equal(time.Millisecond*150, res.Duration)

		//第一个策略超出
		res, err = limiter.Get(id, policy...)
		assert.Equal(2, res.Total)
		assert.Equal(0, res.Remaining)
		assert.Equal(time.Millisecond*150, res.Duration)

		res, err = limiter.Get(id, policy...)
		assert.Equal(2, res.Total)
		assert.Equal(-1, res.Remaining)
		assert.Equal(time.Millisecond*150, res.Duration)

		// 超出后，等待第一个策略过期。
		time.Sleep(res.Duration + time.Millisecond)
		// 如果在第一个策略2倍时间内访问，走第二个策略。 如果不在恢复到第一个策略
		res, err = limiter.Get(id, policy...)
		assert.Equal(2, res.Total)
		assert.Equal(1, res.Remaining)
		assert.Equal(time.Millisecond*200, res.Duration)

		// 在第二个策略正常过期后，恢复到第一个策略
		time.Sleep(res.Duration + time.Millisecond)
		res, err = limiter.Get(id, policy...)
		assert.Equal(2, res.Total)
		assert.Equal(1, res.Remaining)
		assert.Equal(time.Millisecond*150, res.Duration)

		//第一个策略又超出
		res, err = limiter.Get(id, policy...)
		assert.Equal(2, res.Total)
		assert.Equal(0, res.Remaining)
		assert.Equal(time.Millisecond*150, res.Duration)
		res, err = limiter.Get(id, policy...)
		assert.Equal(2, res.Total)
		assert.Equal(-1, res.Remaining)
		assert.Equal(time.Millisecond*150, res.Duration)

		//等待第一个策略过期，然后走第二个策略
		time.Sleep(res.Duration + time.Millisecond)
		res, err = limiter.Get(id, policy...)
		assert.Equal(2, res.Total)
		assert.Equal(1, res.Remaining)
		assert.Equal(time.Millisecond*200, res.Duration)

		//第二个策略页超出
		res, err = limiter.Get(id, policy...)
		assert.Equal(2, res.Total)
		assert.Equal(0, res.Remaining)
		assert.Equal(time.Millisecond*200, res.Duration)
		res, err = limiter.Get(id, policy...)
		assert.Equal(-1, res.Remaining)

		//等待第二个过期，走第三个，然后第三个超出
		time.Sleep(res.Duration + time.Millisecond)
		res, err = limiter.Get(id, policy...)
		assert.Equal(3, res.Total)
		assert.Equal(2, res.Remaining)
		assert.Equal(time.Millisecond*300, res.Duration)

		res, err = limiter.Get(id, policy...)
		assert.Equal(3, res.Total)
		assert.Equal(1, res.Remaining)
		assert.Equal(time.Millisecond*300, res.Duration)
		res, err = limiter.Get(id, policy...)

		assert.Equal(3, res.Total)
		assert.Equal(0, res.Remaining)
		assert.Equal(time.Millisecond*300, res.Duration)
		res, err = limiter.Get(id, policy...)
		assert.Equal(-1, res.Remaining)

		//等待第三个过期，走第四个，然后第四个也过期
		time.Sleep(res.Duration + time.Millisecond)
		res, err = limiter.Get(id, policy...)
		assert.Equal(3, res.Total)
		assert.Equal(2, res.Remaining)
		assert.Equal(time.Millisecond*400, res.Duration)

		res, err = limiter.Get(id, policy...)
		assert.Equal(3, res.Total)
		assert.Equal(1, res.Remaining)

		res, err = limiter.Get(id, policy...)
		assert.Equal(3, res.Total)
		assert.Equal(0, res.Remaining)

		res, err = limiter.Get(id, policy...)
		assert.Equal(3, res.Total)
		assert.Equal(-1, res.Remaining)

		//等待第四个策略过期，还是走第四个策略，因为还在第三个策略2倍时间内
		time.Sleep(res.Duration + time.Millisecond)
		res, err = limiter.Get(id, policy...)
		assert.Equal(3, res.Total)
		assert.Equal(2, res.Remaining)
		assert.Equal(time.Millisecond*400, res.Duration)

		//第四个策略第二次过期，恢复走第一个。
		time.Sleep(res.Duration + time.Millisecond)
		res, err = limiter.Get(id, policy...)
		assert.Equal(2, res.Total)
		assert.Equal(1, res.Remaining)
		assert.Equal(time.Millisecond*150, res.Duration)

	})
	t.Run("limiter.Get with different policy time situation for expired", func(t *testing.T) {
		assert := assert.New(t)

		var id = genID()

		limiter := ratelimiter.New(ratelimiter.Options{
			Client: &redisClient{client},
		})

		policy := []int{2, 300, 3, 100}

		//默认走第一个策略
		res, err := limiter.Get(id, policy...)
		assert.Nil(err)
		assert.Equal(2, res.Total)
		assert.Equal(1, res.Remaining)
		assert.Equal(time.Millisecond*300, res.Duration)

		//第一个策略超出
		res, err = limiter.Get(id, policy...)
		res, err = limiter.Get(id, policy...)
		assert.Equal(-1, res.Remaining)
		assert.Equal(time.Millisecond*300, res.Duration)

		//等待第一个策略过期，   然后走第二个策略，
		time.Sleep(res.Duration + time.Millisecond)
		res, err = limiter.Get(id, policy...)
		assert.Equal(3, res.Total)
		assert.Equal(2, res.Remaining)
		assert.Equal(time.Millisecond*100, res.Duration)

		//第一次正常过期，
		time.Sleep(res.Duration + time.Millisecond)
		res, err = limiter.Get(id, policy...)
		assert.Equal(3, res.Total)
		assert.Equal(2, res.Remaining)
		assert.Equal(time.Millisecond*100, res.Duration)

		///第二次正常过期
		time.Sleep(res.Duration + time.Millisecond)
		res, err = limiter.Get(id, policy...)
		assert.Equal(3, res.Total)
		assert.Equal(2, res.Remaining)
		assert.Equal(time.Millisecond*100, res.Duration)

		///第三次正常过期，恢复到第一个
		time.Sleep(res.Duration + time.Millisecond)
		res, err = limiter.Get(id, policy...)
		assert.Nil(err)
		assert.Equal(2, res.Total)
		assert.Equal(1, res.Remaining)
		assert.Equal(time.Millisecond*300, res.Duration)

		//==========然后第一个策略又超出了
		res, err = limiter.Get(id, policy...)
		res, err = limiter.Get(id, policy...)
		assert.Equal(-1, res.Remaining)
		assert.Equal(time.Millisecond*300, res.Duration)

		//等待第一个策略过期，
		time.Sleep(res.Duration + time.Millisecond)
		//走第二个策略（第一次），
		res, err = limiter.Get(id, policy...)
		assert.Equal(3, res.Total)
		assert.Equal(2, res.Remaining)
		assert.Equal(time.Millisecond*100, res.Duration)

		// 第二个策略超过，
		res, err = limiter.Get(id, policy...)
		assert.Equal(3, res.Total)
		assert.Equal(1, res.Remaining)
		assert.Equal(time.Millisecond*100, res.Duration)
		res, err = limiter.Get(id, policy...)
		assert.Equal(3, res.Total)
		assert.Equal(0, res.Remaining)
		assert.Equal(time.Millisecond*100, res.Duration)

		//等待过期
		time.Sleep(res.Duration + time.Millisecond)

		//走第二个策略（第二次），在第二个策略二倍时间内
		res, err = limiter.Get(id, policy...)
		assert.Equal(3, res.Total)
		assert.Equal(2, res.Remaining)
		assert.Equal(time.Millisecond*100, res.Duration)

		//第二个策略继续超出，延长2倍时间
		res, err = limiter.Get(id, policy...)
		assert.Equal(3, res.Total)
		assert.Equal(1, res.Remaining)
		assert.Equal(time.Millisecond*100, res.Duration)
		res, err = limiter.Get(id, policy...)
		assert.Equal(3, res.Total)
		assert.Equal(0, res.Remaining)
		assert.Equal(time.Millisecond*100, res.Duration)

		//等待过期
		time.Sleep(res.Duration + time.Millisecond)
		//然后走第二个策略，在第二个策略二倍时间内（被延长过）。  如果一直超出被停留在第二次
		res, err = limiter.Get(id, policy...)
		assert.Equal(3, res.Total)
		assert.Equal(2, res.Remaining)
		assert.Equal(time.Millisecond*100, res.Duration)

		//第二个策略第二次过期了，没有被延长
		time.Sleep(res.Duration + time.Millisecond)
		//恢复到第一个
		time.Sleep(res.Duration + time.Millisecond)
		res, err = limiter.Get(id, policy...)
		assert.Nil(err)
		assert.Equal(2, res.Total)
		assert.Equal(1, res.Remaining)
		assert.Equal(time.Millisecond*300, res.Duration)
	})
	t.Run("limiter.Get with normal situation for expired", func(t *testing.T) {
		assert := assert.New(t)

		var id = genID()
		limiter := ratelimiter.New(ratelimiter.Options{
			Client: &redisClient{client},
		})

		policy := []int{3, 300, 2, 200}

		res, err := limiter.Get(id, policy...)
		assert.Nil(err)
		assert.Equal(3, res.Total)
		assert.Equal(2, res.Remaining)
		assert.Equal(time.Millisecond*300, res.Duration)

		res, err = limiter.Get(id, policy...)
		assert.Equal(3, res.Total)
		assert.Equal(1, res.Remaining)
		assert.Equal(time.Millisecond*300, res.Duration)

		res, err = limiter.Get(id, policy...)
		assert.Equal(3, res.Total)
		assert.Equal(0, res.Remaining)
		assert.Equal(time.Millisecond*300, res.Duration)
		res, err = limiter.Get(id, policy...)
		assert.Equal(-1, res.Remaining)

		//等待过期，然后走第二个
		time.Sleep(res.Duration + time.Millisecond)
		res, err = limiter.Get(id, policy...)
		assert.Equal(2, res.Total)
		assert.Equal(1, res.Remaining)
		assert.Equal(time.Millisecond*200, res.Duration)

		//第二策略正常过期
		time.Sleep(res.Duration + time.Millisecond)
		res, err = limiter.Get(id, policy...)
		assert.Equal(2, res.Total)
		assert.Equal(1, res.Remaining)
		assert.Equal(time.Millisecond*200, res.Duration)

		//第二策略第二次正常过期，恢复到第一个
		time.Sleep(res.Duration + time.Millisecond)
		res, err = limiter.Get(id, policy...)
		assert.Equal(3, res.Total)
		assert.Equal(2, res.Remaining)
		assert.Equal(time.Millisecond*300, res.Duration)

	})
	t.Run("ratelimiter.New, Chaos", func(t *testing.T) {
		t.Run("10 limiters work for one id", func(t *testing.T) {
			assert := assert.New(t)

			var wg sync.WaitGroup
			var id = genID()
			var result = NewResult(make([]int, 10000))
			var redisOptions = redis.Options{Addr: "localhost:6379"}

			var worker = func(c *redis.Client, l *ratelimiter.Limiter) {
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
				limiter := ratelimiter.New(ratelimiter.Options{Client: &redisClient{client}, Max: 9998})
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
			assert := assert.New(t)

			var wg sync.WaitGroup
			var id = genID()
			var result = NewResult(make([]int, 10000))
			var redisOptions = redis.RingOptions{Addrs: map[string]string{
				"a": "localhost:6379",
				"b": "localhost:6380",
			}}

			var worker = func(c *redis.Ring, l *ratelimiter.Limiter) {
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
				limiter := ratelimiter.New(ratelimiter.Options{Client: &ringClient{client}, Max: 9998})
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
			assert := assert.New(t)

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

			var worker = func(c *redis.ClusterClient, l *ratelimiter.Limiter) {
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
				limiter := ratelimiter.New(ratelimiter.Options{Client: &clusterClient{client}, Max: 9998})
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
	t.Run("ratelimiter with no redis machine should be", func(t *testing.T) {
		assert := assert.New(t)
		var client = redis.NewClient(&redis.Options{
			Addr: "localhost:6399",
		})
		assert.Panics(func() {
			ratelimiter.New(ratelimiter.Options{Client: &redisClient{client}})
		})
	})
	t.Run("ratelimiter with redisFailedClient should be", func(t *testing.T) {
		assert := assert.New(t)

		var limiter *ratelimiter.Limiter
		var client = redis.NewClient(&redis.Options{
			Addr: "localhost:6379",
		})

		t.Run("ratelimiter.New", func(t *testing.T) {
			limiter = ratelimiter.New(ratelimiter.Options{Client: &redisFailedClient{client}})
		})
		policy := []int{2, 100, 2, 200, 1, 300}
		id := genID()
		res, err := limiter.Get(id, policy...)
		assert.Equal("NOSCRIPT mock error", err.Error())

		assert.Equal(0, res.Total)
		assert.Equal(0, res.Remaining)
		assert.Equal(time.Duration(0), res.Duration)

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
