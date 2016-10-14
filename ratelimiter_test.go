package ratelimiter_test

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/teambition/ratelimiter-go"
	"gopkg.in/redis.v5"
)

func TestRatelimiterGo(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "RatelimiterGo Suite")
}

var client *redis.Client
var limiter *ratelimiter.Limiter

var _ = BeforeSuite(func() {
	client = redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	pong, err := client.Ping().Result()
	Expect(pong).To(Equal("PONG"))
	Expect(err).ToNot(HaveOccurred())
})

var _ = AfterSuite(func() {
	err := client.Close()
	Expect(err).ShouldNot(HaveOccurred())
})

var _ = Describe("RatelimiterGo", func() {
	var _ = Describe("ratelimiter.New, With default options", func() {
		var limiter *ratelimiter.Limiter
		var id string = genID()

		It("ratelimiter.New", func() {
			res, err := ratelimiter.New(client, ratelimiter.Options{})
			Expect(err).ToNot(HaveOccurred())
			limiter = res
		})

		It("limiter.Get", func() {
			res, err := limiter.Get(id)
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Total).To(Equal(100))
			Expect(res.Remaining).To(Equal(99))
			Expect(res.Duration).To(Equal(time.Duration(60 * 1e9)))
			Expect(res.Reset.UnixNano() > time.Now().UnixNano()).To(Equal(true))

			res2, err2 := limiter.Get(id)
			Expect(err2).ToNot(HaveOccurred())
			Expect(res2.Total).To(Equal(100))
			Expect(res2.Remaining).To(Equal(98))
		})

		It("limiter.Remove", func() {
			res, err := limiter.Remove(id)
			Expect(err).ToNot(HaveOccurred())
			Expect(res).To(Equal(1))

			res2, err2 := limiter.Remove(id)
			Expect(err2).ToNot(HaveOccurred())
			Expect(res2).To(Equal(0))

			res3, err3 := limiter.Get(id)
			Expect(err3).ToNot(HaveOccurred())
			Expect(res3.Total).To(Equal(100))
			Expect(res3.Remaining).To(Equal(99))
		})

		It("limiter.Get with invalid args", func() {
			_, err := limiter.Get(id, 10)
			Expect(err.Error()).To(Equal("ratelimiter: must be paired values"))

			_, err2 := limiter.Get(id, -1, 10)
			Expect(err2.Error()).To(Equal("ratelimiter: must be positive integer"))

			_, err3 := limiter.Get(id, 10, 0)
			Expect(err3.Error()).To(Equal("ratelimiter: must be positive integer"))
		})
	})

	var _ = Describe("ratelimiter.New, With options", func() {
		var limiter *ratelimiter.Limiter
		var id string = genID()

		It("ratelimiter.New", func() {
			res, err := ratelimiter.New(client, ratelimiter.Options{
				Max:      3,
				Duration: time.Second,
			})
			Expect(err).ToNot(HaveOccurred())
			limiter = res
		})

		It("limiter.Get", func() {
			res, err := limiter.Get(id)
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Total).To(Equal(3))
			Expect(res.Remaining).To(Equal(2))
			Expect(res.Duration).To(Equal(time.Second))
			Expect(res.Reset.UnixNano() > time.Now().UnixNano()).To(Equal(true))
			Expect(res.Reset.UnixNano() <= time.Now().Add(time.Second).UnixNano()).To(Equal(true))

			res2, _ := limiter.Get(id)
			Expect(res2.Remaining).To(Equal(1))
			res3, _ := limiter.Get(id)
			Expect(res3.Remaining).To(Equal(0))
			res4, _ := limiter.Get(id)
			Expect(res4.Remaining).To(Equal(0))
		})

		It("limiter.Remove", func() {
			res, err := limiter.Remove(id)
			Expect(err).ToNot(HaveOccurred())
			Expect(res).To(Equal(1))

			res2, err2 := limiter.Get(id)
			Expect(err2).ToNot(HaveOccurred())
			Expect(res2.Remaining).To(Equal(2))
		})

		It("limiter.Get with multi-policy", func() {
			id := genID()
			policy := []int{2, 500, 2, 1000, 1, 1000}

			res, err := limiter.Get(id, policy...)
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Total).To(Equal(2))
			Expect(res.Remaining).To(Equal(1))
			Expect(res.Duration).To(Equal(time.Millisecond * 500))

			res2, err2 := limiter.Get(id, policy...)
			Expect(err2).ToNot(HaveOccurred())
			Expect(res2.Remaining).To(Equal(0))

			time.Sleep(res2.Duration + time.Millisecond)
			res3, err3 := limiter.Get(id, policy...)
			Expect(err3).ToNot(HaveOccurred())
			Expect(res3.Total).To(Equal(2))
			Expect(res3.Remaining).To(Equal(1))
			Expect(res3.Duration).To(Equal(time.Second))

			res4, err4 := limiter.Get(id, policy...)
			Expect(err4).ToNot(HaveOccurred())
			Expect(res4.Remaining).To(Equal(0))

			time.Sleep(res4.Duration + time.Millisecond)
			res5, err5 := limiter.Get(id, policy...)
			Expect(err5).ToNot(HaveOccurred())
			Expect(res5.Total).To(Equal(1))
			Expect(res5.Remaining).To(Equal(0))
			Expect(res5.Duration).To(Equal(time.Second))

			// restore after double Duration
			time.Sleep(res4.Duration*2 + time.Millisecond)
			res6, err6 := limiter.Get(id, policy...)
			Expect(err6).ToNot(HaveOccurred())
			Expect(res6.Total).To(Equal(2))
			Expect(res6.Remaining).To(Equal(1))
			Expect(res6.Duration).To(Equal(time.Millisecond * 500))
		})
	})

	var _ = Describe("ratelimiter.New, Chaos", func() {
		It("10 limiters work for one id", func() {
			var wg sync.WaitGroup
			var id string = genID()
			var result = NewResult(make([]int, 10000))

			var redisOptions = redis.Options{Addr: "localhost:6379"}
			var limiterOptions = ratelimiter.Options{Max: 9999}
			var worker = func(c *redis.Client, l *ratelimiter.Limiter) {
				defer wg.Done()
				defer c.Close()

				for i := 0; i < 1000; i++ {
					res, err := l.Get(id)
					Expect(err).ToNot(HaveOccurred())
					result.Push(res.Remaining)
				}
			}

			wg.Add(10)
			for i := 0; i < 10; i++ {
				client := redis.NewClient(&redisOptions)
				limiter, err := ratelimiter.New(client, limiterOptions)
				Expect(err).ToNot(HaveOccurred())
				go worker(client, limiter)
			}

			wg.Wait()
			s := result.Value()
			sort.Ints(s) // [0 0 1 2 3 ... 9997 9998]
			Expect(s[0]).To(Equal(0))
			for i := 1; i < 10000; i++ {
				Expect(s[i]).To(Equal(i - 1))
			}
		})
	})

	var _ = Describe("ratelimiter.RingNew, Chaos", func() {
		It("10 limiters work for one id", func() {
			Skip("Can't run in travis")

			var wg sync.WaitGroup
			var id string = genID()
			var result = NewResult(make([]int, 10000))

			var redisOptions = redis.RingOptions{Addrs: map[string]string{
				"a": "localhost:6379",
				"b": "localhost:6380",
			}}
			var limiterOptions = ratelimiter.Options{Max: 9999}
			var worker = func(c *redis.Ring, l *ratelimiter.Limiter) {
				defer wg.Done()
				defer c.Close()

				for i := 0; i < 1000; i++ {
					res, err := l.Get(id)
					Expect(err).ToNot(HaveOccurred())
					result.Push(res.Remaining)
				}
			}

			wg.Add(10)
			for i := 0; i < 10; i++ {
				client := redis.NewRing(&redisOptions)
				limiter, err := ratelimiter.RingNew(client, limiterOptions)
				Expect(err).ToNot(HaveOccurred())
				go worker(client, limiter)
			}

			wg.Wait()
			s := result.Value()
			sort.Ints(s) // [0 0 1 2 3 ... 9997 9998]
			Expect(s[0]).To(Equal(0))
			for i := 1; i < 10000; i++ {
				Expect(s[i]).To(Equal(i - 1))
			}
		})
	})

	var _ = Describe("ratelimiter.ClusterNew, Chaos", func() {
		It("10 limiters work for one id", func() {
			Skip("Can't run in travis")

			var wg sync.WaitGroup
			var id string = genID()
			var result = NewResult(make([]int, 10000))

			var redisOptions = redis.ClusterOptions{Addrs: []string{
				"localhost:7000",
				"localhost:7001",
				"localhost:7002",
				"localhost:7003",
				"localhost:7004",
				"localhost:7005",
			}}
			var limiterOptions = ratelimiter.Options{Max: 9999}
			var worker = func(c *redis.ClusterClient, l *ratelimiter.Limiter) {
				defer wg.Done()
				defer c.Close()

				for i := 0; i < 1000; i++ {
					res, err := l.Get(id)
					Expect(err).ToNot(HaveOccurred())
					result.Push(res.Remaining)
				}
			}

			wg.Add(10)
			for i := 0; i < 10; i++ {
				client := redis.NewClusterClient(&redisOptions)
				limiter, err := ratelimiter.ClusterNew(client, limiterOptions)
				Expect(err).ToNot(HaveOccurred())
				go worker(client, limiter)
			}

			wg.Wait()
			s := result.Value()
			sort.Ints(s) // [0 0 1 2 3 ... 9997 9998]
			Expect(s[0]).To(Equal(0))
			for i := 1; i < 10000; i++ {
				Expect(s[i]).To(Equal(i - 1))
			}
		})
	})
})

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
