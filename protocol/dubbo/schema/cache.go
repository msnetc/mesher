package schema

import (
	"strings"
	"sync"
	"time"

	"github.com/ServiceComb/go-chassis/core/config"
	"github.com/ServiceComb/go-chassis/core/lager"
	"github.com/ServiceComb/go-chassis/core/registry"
	cache "github.com/patrickmn/go-cache"
)

var (
	svcToInterfaceCache *cache.Cache
	protoCache          *cache.Cache

	DefaultInterval   = 30 * time.Second
	DefaultExpireTime = 0 * time.Second

	refresher *refreshTicker
)

func init() {
	svcToInterfaceCache = cache.New(DefaultExpireTime, 0)
	protoCache = cache.New(DefaultExpireTime, 0)

	refresher = newRefresher(DefaultInterval)
	refresher.Run()
}

func newInterfaceJob(interfaceName string) Job {
	return Job{Fn: func() {
		svc := registry.DefaultContractDiscoveryService.GetMicroServicesByInterface(interfaceName)
		if svc != nil {
			svcKey := strings.Join([]string{svc[0].ServiceName, svc[0].Version, svc[0].AppID}, "/")
			lager.Logger.Infof("refresh cache svc [%s] for interface %s", svcKey, interfaceName)
			svcToInterfaceCache.Set(interfaceName, svc[0], 0)
		}
	}}
}

func newProtoJob(serviceID string) Job {
	return Job{Fn: func() {
		ins, err := registry.DefaultServiceDiscoveryService.GetMicroServiceInstances(config.SelfServiceID, serviceID)
		if err == nil {
			proto := protoForService(ins)
			lager.Logger.Infof("refresh cache proto %s for serviceID %s", proto, serviceID)
			protoCache.Set(serviceID, proto, 0)
		}
	}}
}

func protoForService(ins []*registry.MicroServiceInstance) string {
	proto := "dubbo"
	for _, in := range ins {
		if _, ok := in.EndpointsMap[proto]; !ok {
			proto = "rest"
			break
		}
	}
	return proto
}

func newRefresher(t time.Duration) *refreshTicker {
	return &refreshTicker{
		jobs: Queue{
			tick: t,
			cond: sync.NewCond(&sync.Mutex{}),
			q:    make([]Job, 0)},
	}
}

type refreshTicker struct {
	jobs Queue
}
type Queue struct {
	tick time.Duration
	cond *sync.Cond
	q    []Job
}

type Job struct {
	Fn   JobFunc
	Next time.Time
}
type JobFunc func()

func (tc *refreshTicker) Add(job Job) { tc.jobs.Push(job) }
func (tc *refreshTicker) Run()        { go tc.run() }

func (tc *refreshTicker) run() {
	var timer *time.Timer
	var top Job
	for {
		top = tc.jobs.Top()
		timer = time.NewTimer(top.Next.Sub(time.Now()))
		//TODO: if top is earlier than now
		select {
		case <-timer.C:
			top = tc.jobs.Pop()
			go top.Fn()
			tc.jobs.Push(top)
		}
	}
}

func (q *Queue) Push(x Job) {
	q.cond.L.Lock()
	defer q.cond.L.Unlock()
	x.Next = time.Now().Add(q.tick)
	q.q = append(q.q, x)
	q.cond.Signal()
}

func (q *Queue) Top() Job {
	q.cond.L.Lock()
	defer q.cond.L.Unlock()
	for len(q.q) == 0 {
		q.cond.Wait()
	}

	return q.q[0]
}

func (q *Queue) Pop() Job {
	q.cond.L.Lock()
	defer q.cond.L.Unlock()
	for len(q.q) == 0 {
		q.cond.Wait()
	}

	x := q.q[0]
	q.q = q.q[1:]
	return x
}
