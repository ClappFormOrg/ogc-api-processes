package store

import (
	"fmt"
	"ogc-api/models"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Store holds registered processes and submitted jobs in memory.
type Store struct {
	mu        sync.RWMutex
	processes map[string]models.ProcessDescription
	jobs      map[string]*Job
	jobOrder  []string // insertion order
	counter   int
}

// Job wraps status info with the actual result payload.
type Job struct {
	Status       models.StatusInfo
	Result       interface{} // stored result once complete
	ErrStatus    int         // HTTP status describing a failure; 0 unless failed
	LastAccessed time.Time
}

// New creates an empty Store. Processes are registered via handlers.NewProcessHandlers.
func New() *Store {
	return &Store{
		processes: make(map[string]models.ProcessDescription),
		jobs:      make(map[string]*Job),
	}
}

// ListProcesses returns all registered processes, sorted by ID so that
// paginated process lists are stable.
func (s *Store) ListProcesses() []models.ProcessSummary {
	s.mu.RLock()
	defer s.mu.RUnlock()
	list := make([]models.ProcessSummary, 0, len(s.processes))
	for _, p := range s.processes {
		list = append(list, p.ProcessSummary)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	return list
}

// GetProcess returns a single process description.
func (s *Store) GetProcess(id string) (models.ProcessDescription, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.processes[id]
	return p, ok
}

// RegisterProcess adds or replaces a process definition.
func (s *Store) RegisterProcess(p models.ProcessDescription) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.processes[p.ID] = p
}

// CreateJob creates a new job for the given process and returns its ID.
func (s *Store) CreateJob(processID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Generate uuid for job id
	id := fmt.Sprintf("job-%s", uuid.New().String())
	now := time.Now().UTC()
	j := &Job{
		Status: models.StatusInfo{
			ID:                   id,
			ProcessID:            processID,
			Status:               models.JobStatusAccepted,
			ProcessingEntityType: models.ProcessingEntityType,
			Created:              &now,
		},
		LastAccessed: now,
	}
	s.jobs[id] = j
	s.jobOrder = append(s.jobOrder, id)
	return id
}

// GetJob returns a snapshot of a job by ID and updates its LastAccessed
// timestamp. The copy keeps callers from racing with concurrent updates.
func (s *Store) GetJob(id string) (*Job, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return nil, false
	}
	j.LastAccessed = time.Now().UTC()
	c := *j
	return &c, true
}

// UpdateJob updates job status, timestamps, and optionally stores a result.
func (s *Store) UpdateJob(id, status string, result interface{}) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return false
	}
	now := time.Now().UTC()
	j.Status.Status = status
	j.Status.Updated = &now
	if status == models.JobStatusRunning && j.Status.Started == nil {
		j.Status.Started = &now
	}
	if status == models.JobStatusSuccessful || status == models.JobStatusFailed {
		j.Status.Finished = &now
	}
	if result != nil {
		j.Result = result
	}
	return true
}

// DismissJob marks a job as dismissed.
func (s *Store) DismissJob(id string) bool {
	return s.UpdateJob(id, models.JobStatusDismissed, nil)
}

// FailJob marks a job as failed with a human-readable message and the HTTP
// status that GET /jobs/{id}/results should report for the failure.
// exception, when non-nil, is exposed as statusInfo.exception.
func (s *Store) FailJob(id string, httpStatus int, message string, exception *models.ErrorResponse) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return false
	}
	now := time.Now().UTC()
	j.Status.Status = models.JobStatusFailed
	j.Status.Message = message
	j.Status.Exception = exception
	j.Status.Updated = &now
	j.Status.Finished = &now
	j.ErrStatus = httpStatus
	return true
}

// RemoveJob deletes a job and its results.
func (s *Store) RemoveJob(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.jobs[id]; !ok {
		return false
	}
	delete(s.jobs, id)
	s.jobOrder = slices.DeleteFunc(s.jobOrder, func(v string) bool { return v == id })
	return true
}

// JobFilter selects jobs for GET /jobs (job-list conformance class, 2.0).
// Empty slices and nil pointers mean "no constraint".
type JobFilter struct {
	ProcessIDs  []string
	Statuses    []string
	From, To    *time.Time // inclusive bounds on created; nil = open
	MinDuration *time.Duration
	MaxDuration *time.Duration
}

// FilterJobs returns all jobs matching f, newest first.
func (s *Store) FilterJobs(f JobFilter) []models.StatusInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	now := time.Now().UTC()
	var out []models.StatusInfo
	for i := len(s.jobOrder) - 1; i >= 0; i-- {
		j, ok := s.jobs[s.jobOrder[i]]
		if !ok || !f.matches(j.Status, now) {
			continue
		}
		out = append(out, j.Status)
	}
	return out
}

func (f JobFilter) matches(st models.StatusInfo, now time.Time) bool {
	if len(f.ProcessIDs) > 0 && !slices.ContainsFunc(f.ProcessIDs, func(want string) bool {
		// processID is stored as a process URL; accept the bare ID too.
		return st.ProcessID == want || strings.HasSuffix(st.ProcessID, "/processes/"+want)
	}) {
		return false
	}
	if len(f.Statuses) > 0 && !slices.Contains(f.Statuses, st.Status) {
		return false
	}
	if st.Created != nil {
		if f.From != nil && st.Created.Before(*f.From) {
			return false
		}
		if f.To != nil && st.Created.After(*f.To) {
			return false
		}
	}
	if f.MinDuration == nil && f.MaxDuration == nil {
		return true
	}
	// Duration filters only consider jobs that have started: running jobs are
	// measured up to now, finished jobs up to their finish time.
	if st.Started == nil {
		return false
	}
	end := now
	if st.Finished != nil {
		end = *st.Finished
	}
	d := end.Sub(*st.Started)
	if f.MinDuration != nil && d < *f.MinDuration {
		return false
	}
	if f.MaxDuration != nil && d > *f.MaxDuration {
		return false
	}
	return true
}

// StartCleanup runs a background goroutine that removes jobs not accessed
// within maxAge. It ticks every interval until the returned stop func is called.
func (s *Store) StartCleanup(interval, maxAge time.Duration) (stop func()) {
	ticker := time.NewTicker(interval)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-ticker.C:
				s.evictStale(maxAge)
			case <-done:
				ticker.Stop()
				return
			}
		}
	}()
	return func() { close(done) }
}

func (s *Store) evictStale(maxAge time.Duration) {
	cutoff := time.Now().UTC().Add(-maxAge)
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, j := range s.jobs {
		if j.LastAccessed.Before(cutoff) {
			delete(s.jobs, id)
		}
	}
	// Rebuild jobOrder, preserving insertion order for surviving jobs.
	kept := s.jobOrder[:0]
	for _, id := range s.jobOrder {
		if _, ok := s.jobs[id]; ok {
			kept = append(kept, id)
		}
	}
	s.jobOrder = kept
}

// ListJobs returns jobs in insertion order, newest first.
func (s *Store) ListJobs(limit, offset int) []models.StatusInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	total := len(s.jobOrder)
	if offset >= total {
		return nil
	}

	// Reverse order (newest first)
	reversed := make([]string, total)
	for i, id := range s.jobOrder {
		reversed[total-1-i] = id
	}

	end := offset + limit
	if end > total {
		end = total
	}
	slice := reversed[offset:end]

	result := make([]models.StatusInfo, 0, len(slice))
	for _, id := range slice {
		if j, ok := s.jobs[id]; ok {
			result = append(result, j.Status)
		}
	}
	return result
}
