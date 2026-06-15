package scheduler

import (
	"fmt"
	"log"
	"sync"
	"oracle-diff-monitor/internal/models"
	"oracle-diff-monitor/internal/store"

	"github.com/robfig/cron/v3"
)

type JobFunc func(pairID int64)

type Scheduler struct {
	cron    *cron.Cron
	store   *store.Store
	jobFunc JobFunc
	jobs    map[int64]cron.EntryID
	mu      sync.Mutex
}

func New(s *store.Store, jobFunc JobFunc) *Scheduler {
	return &Scheduler{
		cron:    cron.New(cron.WithSeconds()),
		store:   s,
		jobFunc: jobFunc,
		jobs:    make(map[int64]cron.EntryID),
	}
}

func (s *Scheduler) Start() error {
	schedules, err := s.store.ListSchedules()
	if err != nil {
		return fmt.Errorf("list schedules: %w", err)
	}

	for _, sc := range schedules {
		if sc.Enabled {
			s.addJob(sc)
		}
	}

	s.cron.Start()
	log.Printf("scheduler started with %d jobs", len(s.jobs))
	return nil
}

func (s *Scheduler) Stop() {
	ctx := s.cron.Stop()
	<-ctx.Done()
}

func (s *Scheduler) Reload() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, entryID := range s.jobs {
		s.cron.Remove(entryID)
	}
	s.jobs = make(map[int64]cron.EntryID)

	schedules, err := s.store.ListSchedules()
	if err != nil {
		return fmt.Errorf("list schedules: %w", err)
	}
	for _, sc := range schedules {
		if sc.Enabled {
			s.addJob(sc)
		}
	}
	log.Printf("scheduler reloaded with %d jobs", len(s.jobs))
	return nil
}

func (s *Scheduler) addJob(sc *models.Schedule) {
	pairID := sc.PairID
	entryID, err := s.cron.AddFunc(sc.CronExpr, func() {
		log.Printf("scheduler running job for pair_id=%d (schedule_id=%d)", pairID, sc.ID)
		if s.jobFunc != nil {
			s.jobFunc(pairID)
		}
	})
	if err != nil {
		log.Printf("scheduler: invalid cron expr %q for schedule %d: %v", sc.CronExpr, sc.ID, err)
		return
	}
	s.jobs[sc.ID] = entryID
}

func (s *Scheduler) AddOrUpdate(sc *models.Schedule) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if entryID, ok := s.jobs[sc.ID]; ok {
		s.cron.Remove(entryID)
		delete(s.jobs, sc.ID)
	}
	if sc.Enabled {
		s.addJob(sc)
	}
}

func (s *Scheduler) Remove(scheduleID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if entryID, ok := s.jobs[scheduleID]; ok {
		s.cron.Remove(entryID)
		delete(s.jobs, scheduleID)
	}
}
