package cron

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Schedule struct {
	Kind    string `json:"kind"` // "at" | "every" | "cron"
	AtMS    int64  `json:"atMs,omitempty"`
	EveryMS int64  `json:"everyMs,omitempty"`
	Expr    string `json:"expr,omitempty"`
	TZ      string `json:"tz,omitempty"`
}

type Payload struct {
	Kind    string `json:"kind"` // "agent_turn"
	Message string `json:"message"`
	Deliver bool   `json:"deliver"`
	Channel string `json:"channel,omitempty"`
	To      string `json:"to,omitempty"`
}

type State struct {
	NextRunAtMS int64  `json:"nextRunAtMs,omitempty"`
	LastRunAtMS int64  `json:"lastRunAtMs,omitempty"`
	LastStatus  string `json:"lastStatus,omitempty"`
	LastError   string `json:"lastError,omitempty"`
}

type Job struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Enabled        bool     `json:"enabled"`
	Schedule       Schedule `json:"schedule"`
	Payload        Payload  `json:"payload"`
	State          State    `json:"state"`
	CreatedAtMS    int64    `json:"createdAtMs"`
	UpdatedAtMS    int64    `json:"updatedAtMs"`
	DeleteAfterRun bool     `json:"deleteAfterRun,omitempty"`
}

type Store struct {
	Version int   `json:"version"`
	Jobs    []Job `json:"jobs"`
}

type Service struct {
	storePath string
	onJob     func(ctx context.Context, job Job) (string, error)

	mu      sync.Mutex
	store   Store
	running bool
	timer   *time.Timer
}

func NewService(storePath string, onJob func(ctx context.Context, job Job) (string, error)) *Service {
	return &Service{
		storePath: storePath,
		onJob:     onJob,
		store:     Store{Version: 1, Jobs: nil},
	}
}

func (s *Service) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return nil
	}
	if err := s.loadLocked(); err != nil {
		return err
	}
	s.recomputeNextRunsLocked()
	if err := s.saveLocked(); err != nil {
		return err
	}
	s.running = true
	s.armLocked(ctx)
	return nil
}

func (s *Service) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running = false
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
}

func (s *Service) List(includeDisabled bool) []Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.loadLocked()
	jobs := make([]Job, 0, len(s.store.Jobs))
	for _, j := range s.store.Jobs {
		if includeDisabled || j.Enabled {
			jobs = append(jobs, j)
		}
	}
	return jobs
}

func (s *Service) Add(name string, sched Schedule, payload Payload) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadLocked(); err != nil {
		return Job{}, err
	}
	now := nowMS()
	if err := validateSchedule(sched, now); err != nil {
		return Job{}, err
	}
	nextRun := computeNextRunMS(sched, now)
	if nextRun <= 0 {
		return Job{}, fmt.Errorf("failed to compute next run for schedule kind: %s", sched.Kind)
	}
	j := Job{
		ID:          newID(),
		Name:        name,
		Enabled:     true,
		Schedule:    sched,
		Payload:     payload,
		State:       State{},
		CreatedAtMS: now,
		UpdatedAtMS: now,
	}
	j.State.NextRunAtMS = nextRun
	s.store.Jobs = append(s.store.Jobs, j)
	if err := s.saveLocked(); err != nil {
		return Job{}, err
	}
	return j, nil
}

func (s *Service) Remove(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.loadLocked()
	var out []Job
	removed := false
	for _, j := range s.store.Jobs {
		if j.ID == id {
			removed = true
			continue
		}
		out = append(out, j)
	}
	s.store.Jobs = out
	_ = s.saveLocked()
	return removed
}

func (s *Service) Toggle(id string, disable bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.loadLocked()
	now := nowMS()
	for i := range s.store.Jobs {
		if s.store.Jobs[i].ID != id {
			continue
		}
		s.store.Jobs[i].Enabled = !disable
		if s.store.Jobs[i].Enabled {
			s.store.Jobs[i].State.NextRunAtMS = computeNextRunMS(s.store.Jobs[i].Schedule, now)
		} else {
			s.store.Jobs[i].State.NextRunAtMS = 0
		}
		s.store.Jobs[i].UpdatedAtMS = now
		_ = s.saveLocked()
		return true
	}
	return false
}

func (s *Service) RunNow(ctx context.Context, id string, force bool) (string, error) {
	s.mu.Lock()
	_ = s.loadLocked()
	var job *Job
	for i := range s.store.Jobs {
		if s.store.Jobs[i].ID == id {
			job = &s.store.Jobs[i]
			break
		}
	}
	s.mu.Unlock()
	if job == nil {
		return "", fmt.Errorf("job not found: %s", id)
	}
	if !job.Enabled && !force {
		return "", fmt.Errorf("job disabled: %s (use force)", id)
	}
	return s.execute(ctx, *job)
}

func (s *Service) armLocked(ctx context.Context) {
	if !s.running {
		return
	}
	next := s.nextWakeMSLocked()
	if next <= 0 {
		return
	}
	delay := time.Duration(max64(0, next-nowMS())) * time.Millisecond
	if s.timer != nil {
		s.timer.Stop()
	}
	s.timer = time.AfterFunc(delay, func() {
		_ = s.onTimer(ctx)
	})
}

func (s *Service) onTimer(ctx context.Context) error {
	// Run due jobs and re-arm. Keep lock short.
	var due []Job
	now := nowMS()
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return nil
	}
	_ = s.loadLocked()
	for _, j := range s.store.Jobs {
		if !j.Enabled || j.State.NextRunAtMS <= 0 {
			continue
		}
		if now >= j.State.NextRunAtMS {
			due = append(due, j)
		}
	}
	s.mu.Unlock()

	for _, j := range due {
		_, _ = s.execute(ctx, j)
	}

	s.mu.Lock()
	_ = s.loadLocked()
	s.recomputeNextRunsLocked()
	_ = s.saveLocked()
	s.armLocked(ctx)
	s.mu.Unlock()
	return nil
}

func (s *Service) execute(ctx context.Context, job Job) (string, error) {
	start := nowMS()
	var resp string
	var err error
	if s.onJob != nil {
		resp, err = s.onJob(ctx, job)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.loadLocked()
	for i := range s.store.Jobs {
		if s.store.Jobs[i].ID != job.ID {
			continue
		}
		j := &s.store.Jobs[i]
		updated := nowMS()
		j.State.LastRunAtMS = start
		if err != nil {
			j.State.LastStatus = "error"
			j.State.LastError = err.Error()
		} else {
			j.State.LastStatus = "ok"
			j.State.LastError = ""
		}
		j.UpdatedAtMS = updated

		// One-shot at: disable or delete
		if j.Schedule.Kind == "at" {
			if j.DeleteAfterRun {
				s.store.Jobs = append(s.store.Jobs[:i], s.store.Jobs[i+1:]...)
			} else {
				j.Enabled = false
				j.State.NextRunAtMS = 0
			}
		} else {
			j.State.NextRunAtMS = computeNextRunMS(j.Schedule, updated)
		}
		break
	}
	_ = s.saveLocked()
	return resp, err
}

func (s *Service) loadLocked() error {
	b, err := os.ReadFile(s.storePath)
	if err != nil {
		if os.IsNotExist(err) {
			s.store = Store{Version: 1, Jobs: nil}
			return nil
		}
		return err
	}
	var st Store
	if err := json.Unmarshal(b, &st); err != nil {
		return fmt.Errorf("parse %s: %w", s.storePath, err)
	}
	if st.Version == 0 {
		st.Version = 1
	}
	s.store = st
	return nil
}

func (s *Service) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.storePath), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s.store, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp := s.storePath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.storePath)
}

func (s *Service) recomputeNextRunsLocked() {
	now := nowMS()
	for i := range s.store.Jobs {
		if !s.store.Jobs[i].Enabled {
			s.store.Jobs[i].State.NextRunAtMS = 0
			continue
		}
		s.store.Jobs[i].State.NextRunAtMS = computeNextRunMS(s.store.Jobs[i].Schedule, now)
	}
}

func (s *Service) nextWakeMSLocked() int64 {
	var best int64
	for _, j := range s.store.Jobs {
		if !j.Enabled || j.State.NextRunAtMS <= 0 {
			continue
		}
		if best == 0 || j.State.NextRunAtMS < best {
			best = j.State.NextRunAtMS
		}
	}
	return best
}

func computeNextRunMS(s Schedule, now int64) int64 {
	switch s.Kind {
	case "at":
		if s.AtMS > now {
			return s.AtMS
		}
		return 0
	case "every":
		if s.EveryMS <= 0 {
			return 0
		}
		return now + s.EveryMS
	case "cron":
		if strings.TrimSpace(s.Expr) == "" {
			return 0
		}
		sched, err := parseCron5(strings.TrimSpace(s.Expr))
		if err != nil {
			return 0
		}
		next := sched.Next(time.UnixMilli(now))
		return next.UnixMilli()
	default:
		return 0
	}
}

func nowMS() int64 { return time.Now().UnixMilli() }

func validateSchedule(s Schedule, now int64) error {
	switch s.Kind {
	case "at":
		if s.AtMS <= 0 {
			return fmt.Errorf("at schedule requires a valid timestamp")
		}
		if s.AtMS <= now {
			return fmt.Errorf("at schedule must be in the future")
		}
		return nil
	case "every":
		if s.EveryMS <= 0 {
			return fmt.Errorf("every schedule requires everyMs > 0")
		}
		return nil
	case "cron":
		expr := strings.TrimSpace(s.Expr)
		if expr == "" {
			return fmt.Errorf("cron schedule requires expr")
		}
		if _, err := parseCron5(expr); err != nil {
			return fmt.Errorf("invalid cron expression: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("unknown schedule kind: %s", s.Kind)
	}
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:8])
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
