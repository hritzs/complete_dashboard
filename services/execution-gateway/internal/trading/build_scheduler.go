package trading

import (
	"context"
	"fmt"
	"log"
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

type BuildScheduler struct {
	mu        sync.Mutex
	scheduled map[string]ScheduledBuild
	service   *Service
}

func NewBuildScheduler(service *Service) *BuildScheduler {
	return &BuildScheduler{
		scheduled: make(map[string]ScheduledBuild),
		service:   service,
	}
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

	s.mu.Lock()
	s.scheduled[id] = job
	s.mu.Unlock()

	delay := time.Until(runAt)

	go func() {
		timer := time.NewTimer(delay)
		defer timer.Stop()

		<-timer.C

		log.Printf(
			"[BUILD SCHEDULER] executing id=%s source=%s symbol=%s lots=%d run_at=%s",
			job.ID,
			job.Source,
			job.Request.Symbol,
			job.Request.Lots,
			job.RunAt.Format(time.RFC3339),
		)

		_, err := s.service.ExecuteFinalBuild(
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
			},
		)
		if err != nil {
			log.Printf(
				"[BUILD SCHEDULER] failed id=%s source=%s err=%v",
				job.ID,
				job.Source,
				err,
			)
			return
		}

		log.Printf(
			"[BUILD SCHEDULER] completed id=%s source=%s",
			job.ID,
			job.Source,
		)

		s.mu.Lock()
		delete(s.scheduled, job.ID)
		s.mu.Unlock()
	}()

	return job, nil
}
