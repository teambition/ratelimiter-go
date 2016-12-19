package ratelimiter

import (
	"errors"
	"sync"
	"time"
)

//Options ...
type statusCacheItem struct {
	index  int
	expire time.Time
}

// LimiterCacheItem of limiter
type limiterCacheItem struct {
	total     int
	remaining int
	duration  time.Duration
	expire    time.Time
	lock      sync.Mutex
}

//Memory ...
type Memory struct {
	lock    sync.RWMutex
	status  map[string]*statusCacheItem
	store   map[string]*limiterCacheItem
	ticker  *time.Ticker
	options *Options
}

func newMemoryLimiter(opts Options) (limiter *Memory) {
	limiter = &Memory{
		options: &opts,
	}
	if limiter.options.Duration == 0 {
		limiter.options.Duration = time.Minute
	}
	if limiter.options.Prefix == "" {
		limiter.options.Prefix = "LIMIT:"
	}
	if limiter.options.Max == 0 {
		limiter.options.Max = 100
	}
	limiter.store = make(map[string]*limiterCacheItem)
	limiter.status = make(map[string]*statusCacheItem)

	duration := 60 * time.Second
	limiter.ticker = time.NewTicker(duration)
	go limiter.cleanCache()
	return
}

//Get ...
func (l *Memory) Get(id string, policy ...int) (result Result, err error) {

	key := l.options.Prefix + id

	length := len(policy)
	if odd := length % 2; odd == 1 {
		return result, errors.New("ratelimiter: must be paired values")
	}
	var p []int
	if length == 0 {
		p = []int{l.options.Max, int(l.options.Duration / time.Millisecond)}
	} else {
		p = make([]int, length)
		for i, val := range policy {
			if val <= 0 {
				return result, errors.New("ratelimiter: must be positive integer")
			}
			p[i] = policy[i]
		}
	}
	return l.getResult(key, p...)
}

//Remove ...
func (l *Memory) Remove(id string) error {
	key := l.options.Prefix + id
	statusKey := "{" + key + "}:S"

	l.lock.Lock()
	defer l.lock.Unlock()
	delete(l.store, key)
	delete(l.status, statusKey)
	return nil
}

//Count ...
func (l *Memory) Count() int {

	l.lock.RLock()
	defer l.lock.RUnlock()
	return len(l.store)
}

//Clean ...
func (l *Memory) Clean() {

	l.lock.Lock()
	defer l.lock.Unlock()
	for key, value := range l.store {
		expire := value.expire.Add(value.duration)
		if expire.Before(time.Now()) {
			statusKey := "{" + key + "}:S"
			delete(l.store, key)
			delete(l.status, statusKey)
		}
	}
}

func (l *Memory) getResult(id string, policy ...int) (Result, error) {
	var result Result
	res := l.getLimit(id, policy...)

	res.lock.Lock()
	remaining := res.remaining
	total := res.total
	res.lock.Unlock()

	result = Result{
		Remaining: remaining,
		Total:     total,
		Duration:  res.duration,
		Reset:     res.expire,
	}
	return result, nil
}
func (l *Memory) getLimit(key string, args ...int) (res *limiterCacheItem) {

	policyCount := len(args) / 2
	total := args[0]
	duration := args[1]
	statusKey := "{" + key + "}:S"

	l.lock.Lock()
	var ok bool
	if res, ok = l.store[key]; ok {
		statusItem, _ := l.status[statusKey]
		l.lock.Unlock()

		res.lock.Lock()
		defer res.lock.Unlock()
		if res.expire.Before(time.Now()) {
			if policyCount > 1 {
				if statusItem.expire.Before(time.Now()) {
					statusItem.index = 1
				} else {
					if statusItem.index >= policyCount {
						statusItem.index = policyCount
					} else {
						statusItem.index++
					}
				}
				total = args[(statusItem.index*2)-2]
				duration = args[(statusItem.index*2)-1]
				statusItem.expire = time.Now().Add(time.Duration(duration) * time.Millisecond * 2)
			}
			res.total = total
			res.remaining = total - 1
			res.duration = time.Duration(duration) * time.Millisecond
			res.expire = time.Now().Add(time.Duration(duration) * time.Millisecond)
		} else {
			if res.remaining == -1 {
				return
			}
			res.remaining--
		}
	} else {
		res = &limiterCacheItem{
			total:     total,
			remaining: total - 1,
			duration:  time.Duration(duration) * time.Millisecond,
			expire:    time.Now().Add(time.Duration(duration) * time.Millisecond),
		}
		status := &statusCacheItem{
			index:  1,
			expire: time.Now().Add(time.Duration(duration) * time.Millisecond * 2),
		}
		l.store[key] = res
		l.status[statusKey] = status
		l.lock.Unlock()
	}
	return
}
func (l *Memory) cleanCache() {
	for now := range l.ticker.C {
		var _ = now
		l.Clean()
	}
}
