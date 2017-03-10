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
	statusKey := "{" + key + "}:S"

	m.lock.Lock()
	defer m.lock.Unlock()
	var ok bool
	if res, ok = m.store[key]; !ok {
		res = &limiterCacheItem{
			total:     args[0],
			remaining: args[0] - 1,
			duration:  time.Duration(args[1]) * time.Millisecond,
			expire:    time.Now().Add(time.Duration(args[1]) * time.Millisecond),
		}
		m.store[key] = res
		return
	}
	if res.expire.After(time.Now()) {
		if policyCount > 1 && res.remaining-1 == -1 {
			statusItem, ok := m.status[statusKey]
			if ok {
				statusItem.expire = time.Now().Add(res.duration * 2)
				statusItem.index++
			} else {
				statusItem := &statusCacheItem{
					index:  2,
					expire: time.Now().Add(time.Duration(args[1]) * time.Millisecond * 2),
				}
				m.status[statusKey] = statusItem
			}
		}
		if res.remaining >= 0 {
			res.remaining--
		} else {
			res.remaining = -1
		}
	} else {
		index := 1
		if policyCount > 1 {
			if statusItem, ok := m.status[statusKey]; ok {
				if statusItem.expire.Before(time.Now()) {
					index = 1
				} else if statusItem.index > policyCount {
					index = policyCount
				} else {
					index = statusItem.index
				}
				statusItem.index = index
			}
		}
		total := args[(index*2)-2]
		duration := args[(index*2)-1]
		res.total = total
		res.remaining = total - 1
		res.duration = time.Duration(duration) * time.Millisecond
		res.expire = time.Now().Add(time.Duration(duration) * time.Millisecond)
	}
	return
}

func (m *memoryLimiter) cleanCache() {
	for range m.ticker.C {
		m.clean()
	}
}
