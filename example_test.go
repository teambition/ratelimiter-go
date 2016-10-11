package ratelimiter_test

import (
	"fmt"
	"time"

	"github.com/teambition/ratelimiter-go"
	"gopkg.in/redis.v5"
)

func ExampleRatelimiterGo() {
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	limiter, err := ratelimiter.New(client, &ratelimiter.Options{
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
