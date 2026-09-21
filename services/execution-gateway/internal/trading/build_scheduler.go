package trading

import (
	"context"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"
)

type BuildSource string

const (
	BuildSourceManual    BuildSource = "MANUAL"
	BuildSourceConfig    BuildSource = "CONFIG"
	BuildSourceAutomated BuildSource = "AUTOMATED"
)

type ScheduledBuild struct {
	ID        string
	Source    BuildSource
	RunAt     time.Time
	Request   DeployStraddleRequest
	CreatedAt time.Time
}

// BuildScheduler holds pending scheduled entries IN MEMORY ONLY: a gateway
// restart drops every pending build, so List is how to see what is still
// waiting.
type BuildScheduler struct {
	mu        sync.Mutex
	scheduled map[string]ScheduledBuild
	cancels   map[string]chan struct{}
	service   *Service
}

func NewBuildScheduler(service *Service) *BuildScheduler {
	return &BuildScheduler{
		scheduled: make(map[string]ScheduledBuild),
		cancels:   make(map[string]chan struct{}),
		service:   service,
	}
}

// List returns the pending builds, soonest first.
func (s *BuildScheduler) List() []ScheduledBuild {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ScheduledBuild, 0, len(s.scheduled))
	for _, job := range s.scheduled {
		out = append(out, job)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RunAt.Before(out[j].RunAt) })
	return out
}

// Cancel stops a pending build before it fires. It reports false if the job
// is unknown or has already started.
func (s *BuildScheduler) Cancel(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch, ok := s.cancels[id]
	if !ok {
		return false
	}
	close(ch)
	delete(s.cancels, id)
	delete(s.scheduled, id)
	return true
}

func ParseTodayIST(value string, now time.Time) (time.Time, error) {
	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		return time.Time{}, fmt.Errorf("load Asia/Kolkata timezone: %w", err)
	}

	now = now.In(loc)
	parsed, err := time.ParseInLocation("15:04:05", value, loc)
	if err != nil {
		return time.Time{}, fmt.Errorf(
			"invalid entry_time %q: expected HH:MM:SS",
			value,
		)
	}

	return time.Date(
		now.Year(),
		now.Month(),
		now.Day(),
		parsed.Hour(),
		parsed.Minute(),
		parsed.Second(),
		0,
		loc,
	), nil
}

func (s *BuildScheduler) Schedule(
	source BuildSource,
	runAt time.Time,
	req DeployStraddleRequest,
) (ScheduledBuild, error) {
	if !runAt.After(time.Now()) {
		return ScheduledBuild{}, fmt.Errorf(
			"scheduled time %s is not in the future",
			runAt.Format(time.RFC3339),
		)
	}

	id := fmt.Sprintf(
		"%s_%s_%d",
		source,
		req.Symbol,
		time.Now().UnixNano(),
	)

	job := ScheduledBuild{
		ID:        id,
		Source:    source,
		RunAt:     runAt,
		Request:   req,
		CreatedAt: time.Now(),
	}

	cancelCh := make(chan struct{})
	s.mu.Lock()
	s.scheduled[id] = job
	s.cancels[id] = cancelCh
	s.mu.Unlock()

	delay := time.Until(runAt)
	log.Printf("[BUILD SCHEDULER] scheduled id=%s source=%s symbol=%s expiry=%s lots=%d run_at=%s", id, source, req.Symbol, req.TargetExpiry, req.Lots, runAt.Format(time.RFC3339))

	go func() {
		timer := time.NewTimer(delay)
		defer timer.Stop()

		select {
		case <-cancelCh:
			log.Printf("[BUILD SCHEDULER] cancelled id=%s before it ran", job.ID)
			return
		case <-timer.C:
		}

		// The job is running now: no longer cancellable, and removed from the
		// pending list whether it succeeds or fails (it used to linger after a
		// failure).
		s.mu.Lock()
		delete(s.cancels, job.ID)
		delete(s.scheduled, job.ID)
		s.mu.Unlock()

		log.Printf(
			"[BUILD SCHEDULER] executing id=%s source=%s symbol=%s expiry=%s lots=%d run_at=%s",
			job.ID, job.Source, job.Request.Symbol, job.Request.TargetExpiry, job.Request.Lots, job.RunAt.Format(time.RFC3339),
		)

		resp, err := s.service.ExecuteFinalBuild(
			context.Background(),
			FinalBuildRequest{
				Mode:             BuildMode(job.Source),
				UserID:           job.Request.UserID,
				BrokerName:       job.Request.BrokerName,
				AccountID:        job.Request.AccountID,
				ExchangeSegment:  job.Request.ExchangeSegment,
				ProductType:      job.Request.ProductType,
				Symbol:           job.Request.Symbol,
				TargetExpiry:     job.Request.TargetExpiry,
				Lots:             job.Request.Lots,
				OrderLotsPerCall: job.Request.OrderLotsPerCall,
				DeltaNeutral:     job.Request.DeltaNeutral,
				Risk:             job.Request.Risk,
			},
		)
		if err != nil {
			log.Printf("[BUILD SCHEDULER] failed id=%s source=%s err=%v", job.ID, job.Source, err)
			return
		}

		log.Printf("[BUILD SCHEDULER] completed id=%s source=%s trade=%s status=%s", job.ID, job.Source, resp.TradeUID, resp.Status)
	}()

	return job, nil
}
