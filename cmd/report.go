package main

import (
	"errors"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"sync"
	"time"
)

// IntervalReport is a point in time view of process' CPU usage with three
// figures, WindowRate, LifeTimeRate and CurrentRate.
// WindowRate - an average of samples over several intervals, which effectively
// makes the data smoother.
// StandardDev - standard deviation for samples in this window.
// LifeTimeRate - rate of time spent on CPU over total process' runtime,
// computed over the entire lifetime of process; least volatile.
// CurrentRate - derivative between two interval samples; most volatile.
type IntervalReport struct {
	PID             int              `json:"pid"`
	Role            string           `json:"role"`
	InitTimestamp   time.Time        `json:"first_seen"`
	Timestamp       time.Time        `json:"last_seen"`
	Age             time.Duration    `json:"age"`
	WindowRate      float64          `json:"window_rate"`
	StandardDev     float64          `json:"standard_dev"`
	LifetimeRate    float64          `json:"lifetime_rate"`
	CurrentRate     float64          `json:"current_rate"`
	TimesRestated   uint64           `json:"times_restarted"`
	VirtMemoryBytes uint             `json:"virtual_memory_bytes"`
	RSSBytes        int              `json:"rss_bytes"`
	RateHistogram   map[string]int64 `json:"rate_histogram"`
}

func (i IntervalReport) String() string {

	role := i.Role
	pid := fmt.Sprintf("bro_pid{role=\"%s\"} %d", role, i.PID)
	first_seen := fmt.Sprintf("bro_process_start_seconds{role=\"%s\"} %d", role, i.Timestamp.Unix() )
	age := fmt.Sprintf("bro_process_age_seconds{role=\"%s\"} %d", role, int(i.Age.Seconds()))
	vmem := fmt.Sprintf("bro_virtual_memory_bytes{role=\"%s\"} %d", role, i.VirtMemoryBytes)

	return pid + "\n" + first_seen + "\n" + age + "\n" + vmem + "\n"
}

func startIntervalReport(c <-chan *IntervalReport) {
	// on each tick, print out all summaries to stdout
	tick := time.NewTicker(reportInterval)
	for {
		select {
		case v := <-c:
			if v == nil {
				return
			}
			metricsReport.Insert(v)
		case <-tick.C:
			if !metricsReport.Empty() {
				data, err := metricsReport.ToJSON()
				if err != nil {
					log.Printf("Failed to produce report with: %v", err)
					continue
				}
				fmt.Fprintln(os.Stdout, string(data))
			}
		default:
			<-time.NewTimer(1 * time.Second).C
		}
	}
}

// Summaries is used as a global singleton to keep track of running
// statistics for processes being monitored.
type Summaries struct {
	m   map[string]*IntervalReport
	mtx sync.RWMutex
}

// Insert updates the map with latest interval report.
func (s *Summaries) Insert(r *IntervalReport) {
	s.mtx.Lock()
	defer s.mtx.Unlock()
	s.m[r.Role] = r
}

// Len returns number of available summaries.
func (s *Summaries) Len() int {
	s.mtx.RLock()
	defer s.mtx.RUnlock()
	return len(s.m)
}

// Empty returns true if there are no summaries to report, false otherwise.
func (s *Summaries) Empty() bool {
	s.mtx.RLock()
	defer s.mtx.RUnlock()
	return len(s.m) == 0
}

func (s *Summaries) findRole(role string) *IntervalReport {
	s.mtx.RLock()
	defer s.mtx.RUnlock()
	if v, ok := s.m[role]; ok {
		return v
	}
	return nil
}

// safeIntervalReport converts any NaNs to -1's, because JSON is brain-dead
// and the idiots behind it apparently don't understand that NaNs, -Inf and +Inf
// are actually a thing.
func (s *Summaries) safeIntervalReport(role string) *IntervalReport {
	s.mtx.RLock()
	defer s.mtx.RUnlock()
	var rep, safeRep *IntervalReport
	if rep = s.findRole(role); rep == nil {
		return nil
	}

	safeRep = &IntervalReport{
		PID:             rep.PID,
		Role:            rep.Role,
		InitTimestamp:   rep.InitTimestamp,
		Timestamp:       rep.Timestamp,
		Age:             rep.Age,
		WindowRate:      rep.WindowRate,
		StandardDev:     rep.StandardDev,
		LifetimeRate:    rep.LifetimeRate,
		CurrentRate:     rep.CurrentRate,
		RateHistogram:   rep.RateHistogram,
		TimesRestated:   rep.TimesRestated,
		VirtMemoryBytes: rep.VirtMemoryBytes,
		RSSBytes:        rep.RSSBytes,
	}

	if math.IsNaN(safeRep.CurrentRate) {
		safeRep.CurrentRate = -1
	}
	if math.IsNaN(safeRep.LifetimeRate) {
		safeRep.LifetimeRate = -1
	}
	if math.IsNaN(safeRep.StandardDev) {
		safeRep.StandardDev = -1
	}
	if math.IsNaN(safeRep.WindowRate) {
		safeRep.WindowRate = -1
	}
	return safeRep
}

// RoleToJSON returns serialized version of a single entry from interval
// summaries map, assuming entry is found in the map.
// Multiple concurrent readers are possible, but only one writer is allowed.
func (s *Summaries) RoleToJSON(role string) ([]byte, error) {
	s.mtx.RLock()
	defer s.mtx.RUnlock()
	var rep *IntervalReport

	if rep = s.safeIntervalReport(role); rep == nil {
		return []byte{}, errNoInfoForRole
	}

	return json.Marshal(rep)
}

// ToJSON returns serialized version of the interval summaries map.
// Multiple concurrent readers are possible, but only one writer is allowed.
func (s *Summaries) ToJSON() ([]byte, error) {
	s.mtx.RLock()
	defer s.mtx.RUnlock()
	l := make([]*IntervalReport, 0)
	for role := range s.m {
		l = append(l, s.safeIntervalReport(role))
	}
	return json.Marshal(l)
}

// ToJSON returns serialized version of the interval summaries map.
// Multiple concurrent readers are possible, but only one writer is allowed.
func (s *Summaries) All() ([]*IntervalReport, error) {
	s.mtx.RLock()
	defer s.mtx.RUnlock()
	if s.Len() == 0 {
		return nil, errors.New("zero summaries currently available")
	}
	l := make([]*IntervalReport, s.Len())
	var c = 0
	for role := range s.m {
		// l = append(l, s.safeIntervalReport(role))
		l[c] = s.safeIntervalReport(role)
		fmt.Printf("role => %v\n", role)
		c++
	}
	return l, nil
}
