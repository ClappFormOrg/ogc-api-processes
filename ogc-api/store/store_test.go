package store

import (
	"ogc-api/models"
	"testing"
)

func TestNew_StartsEmpty(t *testing.T) {
	s := New()
	procs := s.ListProcesses()
	if len(procs) != 0 {
		t.Fatalf("expected empty store, got %d processes", len(procs))
	}
}

func TestGetProcess(t *testing.T) {
	s := New()
	s.RegisterProcess(models.ProcessDescription{
		ProcessSummary: models.ProcessSummary{ID: "echo", Version: "1.0.0"},
	})

	p, ok := s.GetProcess("echo")
	if !ok {
		t.Fatal("expected to find 'echo' process")
	}
	if p.ID != "echo" {
		t.Errorf("expected ID 'echo', got %q", p.ID)
	}

	_, ok = s.GetProcess("nonexistent")
	if ok {
		t.Error("expected nonexistent process to not be found")
	}
}

func TestRegisterProcess(t *testing.T) {
	s := New()
	s.RegisterProcess(models.ProcessDescription{
		ProcessSummary: models.ProcessSummary{
			ID:      "custom",
			Version: "1.0.0",
		},
	})

	p, ok := s.GetProcess("custom")
	if !ok {
		t.Fatal("expected to find registered process 'custom'")
	}
	if p.Version != "1.0.0" {
		t.Errorf("expected version '1.0.0', got %q", p.Version)
	}
}

func TestCreateJob(t *testing.T) {
	s := New()
	id := s.CreateJob("echo")

	if id == "" {
		t.Fatal("expected non-empty job ID")
	}

	job, ok := s.GetJob(id)
	if !ok {
		t.Fatal("expected to find created job")
	}
	if job.Status.Status != models.JobStatusAccepted {
		t.Errorf("expected status %q, got %q", models.JobStatusAccepted, job.Status.Status)
	}
	if job.Status.ProcessID != "echo" {
		t.Errorf("expected processID 'echo', got %q", job.Status.ProcessID)
	}
	if job.Status.Created == nil {
		t.Error("expected Created timestamp to be set")
	}
}

func TestUpdateJob(t *testing.T) {
	s := New()
	id := s.CreateJob("echo")

	// Update to running
	ok := s.UpdateJob(id, models.JobStatusRunning, nil)
	if !ok {
		t.Fatal("expected UpdateJob to return true")
	}
	job, _ := s.GetJob(id)
	if job.Status.Status != models.JobStatusRunning {
		t.Errorf("expected status 'running', got %q", job.Status.Status)
	}
	if job.Status.Started == nil {
		t.Error("expected Started timestamp to be set")
	}

	// Update to successful with result
	result := map[string]string{"result": "hello"}
	s.UpdateJob(id, models.JobStatusSuccessful, result)
	job, _ = s.GetJob(id)
	if job.Status.Status != models.JobStatusSuccessful {
		t.Errorf("expected status 'successful', got %q", job.Status.Status)
	}
	if job.Status.Finished == nil {
		t.Error("expected Finished timestamp to be set")
	}
	if job.Result == nil {
		t.Error("expected result to be stored")
	}
}

func TestUpdateJob_NotFound(t *testing.T) {
	s := New()
	ok := s.UpdateJob("fake-id", models.JobStatusRunning, nil)
	if ok {
		t.Error("expected UpdateJob to return false for nonexistent job")
	}
}

func TestDismissJob(t *testing.T) {
	s := New()
	id := s.CreateJob("echo")

	ok := s.DismissJob(id)
	if !ok {
		t.Fatal("expected DismissJob to return true")
	}
	job, _ := s.GetJob(id)
	if job.Status.Status != models.JobStatusDismissed {
		t.Errorf("expected status 'dismissed', got %q", job.Status.Status)
	}
}

func TestListJobs_Order(t *testing.T) {
	s := New()
	id1 := s.CreateJob("echo")
	id2 := s.CreateJob("echo")
	id3 := s.CreateJob("echo")

	jobs := s.ListJobs(10, 0)
	if len(jobs) != 3 {
		t.Fatalf("expected 3 jobs, got %d", len(jobs))
	}
	// Newest first
	if jobs[0].ID != id3 {
		t.Errorf("expected first job to be %s (newest), got %s", id3, jobs[0].ID)
	}
	if jobs[1].ID != id2 {
		t.Errorf("expected middle job to be %s, got %s", id2, jobs[1].ID)
	}
	if jobs[2].ID != id1 {
		t.Errorf("expected last job to be %s (oldest), got %s", id1, jobs[2].ID)
	}
}

func TestListJobs_Pagination(t *testing.T) {
	s := New()
	for i := 0; i < 5; i++ {
		s.CreateJob("echo")
	}

	// Limit 2
	jobs := s.ListJobs(2, 0)
	if len(jobs) != 2 {
		t.Errorf("expected 2 jobs, got %d", len(jobs))
	}

	// Offset 3, limit 10
	jobs = s.ListJobs(10, 3)
	if len(jobs) != 2 {
		t.Errorf("expected 2 jobs, got %d", len(jobs))
	}

	// Offset beyond range
	jobs = s.ListJobs(10, 100)
	if len(jobs) != 0 {
		t.Errorf("expected 0 jobs, got %d", len(jobs))
	}
}
