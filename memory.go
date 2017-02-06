package ratelimiter

import (
	"errors"
	"sync"
	"time"
)

// policy status
type statusCacheItem struct {
	index  int
	expire time.Time
}

// limit status
type limiterCacheItem struct {
	total     int
	remaining int
	duration  time.Duration
	expire    time.Time
}

type memoryLimiter struct {
	max      int
	duration time.Duration
	status   map[string]*statusCacheItem
	store    map[string]*limiterCacheItem
	ticker   *time.Ticker
	lock     sync.Mutex
}

func newMemoryLimiter(opts *Options) *Limiter {
	m := &memoryLimiter{
		max:      opts.Max,
		duration: opts.Duration,
		store:    make(map[string]*limiterCacheItem),
		status:   make(map[string]*statusCacheItem),
		ticker:   time.NewTicker(time.Second),
	}
	go m.cleanCache()
	return &Limiter{m, opts.Prefix}
}

// abstractLimiter interface
func (m *memoryLimiter) getLimit(key string, policy ...int) ([]interface{}, error) {
	length := len(policy)
	var args []int
	if length == 0 {
		args = []int{m.max, int(m.duration / time.Millisecond)}
	} else {
		args = make([]int, length)
		for i, val := range policy {
			if val <= 0 {
				return nil, errors.New("ratelimiter: must be positive integer")
			}
			args[i] = policy[i]
		}
	}

	res := m.getItem(key, args...)
	m.lock.Lock()
	defer m.lock.Unlock()
	return []interface{}{res.remaining, res.total, res.duration, res.expire}, nil
}

// abstractLimiter interface
func (m *memoryLimiter) removeLimit(key string) error {
	statusKey := "{" + key + "}:S"
	m.lock.Lock()
	defer m.lock.Unlock()
	delete(m.store, key)
	delete(m.status, statusKey)
	return nil
}

func (m *memoryLimiter) clean() {
	m.lock.Lock()
	defer m.lock.Unlock()
	start := time.Now()
	expireTime := start.Add(time.Millisecond * 100)
	frequency := 24
	var expired int
	for {
	label:
		for i := 0; i < frequency; i++ {
			for key, value := range m.store {
				if value.expire.Add(value.duration).Before(start) {
					statusKey := "{" + key + "}:S"
					delete(m.store, key)
					delete(m.status, statusKey)
					expired++
				}
				break
			}
		}
		if expireTime.Before(time.Now()) {
			return
		}
		if expired > frequency/4 {
			expired = 0
			goto label
		}
		return
	}
}

func (m *memoryLimiter) getItem(key string, args ...int) (res *limiterCacheItem) {
	policyCount := len(args) / 2
	total := args[0]
	duration := args[1]
	statusKey := "{" + key + "}:S"

	m.lock.Lock()
	defer m.lock.Unlock()
	var ok bool
	if res, ok = m.store[key]; ok {
		statusItem, _ := m.status[statusKey]
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
		m.store[key] = res
		m.status[statusKey] = status
	}
	return
}

func (m *memoryLimiter) cleanCache() {
	for range m.ticker.C {
		m.clean()
	}
}
