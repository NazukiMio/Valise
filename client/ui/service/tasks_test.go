package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"valise/core/vault"
)

func waitLocal(t *testing.T, ch <-chan LocalTask, ok func(LocalTask) bool) LocalTask {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case v, open := <-ch:
			if !open {
				t.Fatal("subscription closed early")
			}
			if ok(v) {
				return v
			}
		case <-deadline:
			t.Fatal("timeout")
		}
	}
}

func TestLocalTasksStartWatchListDedup(t *testing.T) {
	dir := t.TempDir()
	l, err := NewLocalTasks(dir)
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	var runs atomic.Int32
	job := func(ctx context.Context, stage func(string)) (any, error) {
		runs.Add(1)
		stage("pulling")
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return map[string]int{"downloaded": 3}, nil
	}
	subject := LocalTaskSubject{Host: "box", Repo: "usb"}
	task, err := l.Start(LocalTaskRepoSync, subject, "key", job)
	if err != nil || task.State != "running" {
		t.Fatalf("start %+v %v", task, err)
	}
	ch, unsubscribe, err := l.Watch(task.TaskRef)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	waitLocal(t, ch, func(v LocalTask) bool { return v.Stage == "pulling" })
	if again, err := l.Start(LocalTaskRepoSync, subject, "key", job); err != nil || again.TaskRef != task.TaskRef {
		t.Fatalf("dedup %+v %v", again, err)
	}
	if _, err := l.Start(LocalTaskRepoSync, LocalTaskSubject{Host: "box", Repo: "other"}, "key", job); err == nil {
		t.Fatal("key reused for another request")
	}
	list, err := l.List()
	if err != nil || len(list) != 1 || list[0].State != "running" {
		t.Fatalf("list %+v %v", list, err)
	}
	close(release)
	done := waitLocal(t, ch, func(v LocalTask) bool { return v.State == "succeeded" })
	if string(done.Result) != `{"downloaded":3}` {
		t.Fatalf("result %s", done.Result)
	}
	if runs.Load() != 1 {
		t.Fatalf("runs %d", runs.Load())
	}
	// A new process reads the finished task and never reruns it.
	reopened, err := NewLocalTasks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := reopened.Start(LocalTaskRepoSync, subject, "key", job); err != nil || again.State != "succeeded" || runs.Load() != 1 {
		t.Fatalf("restart %+v %v", again, err)
	}
	st, err := os.Stat(filepath.Join(dir, "local-tasks", task.TaskRef.TaskID+".json"))
	if err != nil || st.Mode().Perm() != 0600 {
		t.Fatalf("index mode %v %v", st, err)
	}
}

func TestLocalTasksCancelShutdownAndInterrupted(t *testing.T) {
	dir := t.TempDir()
	l, err := NewLocalTasks(dir)
	if err != nil {
		t.Fatal(err)
	}
	blocking := func(ctx context.Context, stage func(string)) (any, error) {
		stage("pulling")
		<-ctx.Done()
		return nil, errors.New("raw detail SECRET")
	}
	first, err := l.Start("repo_sync", LocalTaskSubject{Repo: "a"}, "a", blocking)
	if err != nil {
		t.Fatal(err)
	}
	ch, unsubscribe, err := l.Watch(first.TaskRef)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	waitLocal(t, ch, func(v LocalTask) bool { return v.Stage == "pulling" })
	got, err := l.Cancel(first.TaskRef)
	if err != nil || got.State != "cancelling" || !got.CancelRequested {
		t.Fatalf("cancelling %+v %v", got, err)
	}
	done := waitLocal(t, ch, func(v LocalTask) bool { return v.State == "cancelled" })
	if done.Error == nil || done.Error.Code != "cancelled" {
		t.Fatalf("cancelled %+v", done)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "local-tasks", first.TaskRef.TaskID+".json"))
	if len(raw) == 0 || strings.Contains(string(raw), "SECRET") {
		t.Fatal("raw error persisted")
	}

	second, err := l.Start("repo_sync", LocalTaskSubject{Repo: "b"}, "b", blocking)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := l.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	stopped, err := l.Get(second.TaskRef)
	if err != nil || stopped.State != "cancelled" || stopped.Error.Code != "client_exit" {
		t.Fatalf("shutdown %+v %v", stopped, err)
	}
	if _, err := l.Start("repo_sync", LocalTaskSubject{Repo: "c"}, "c", blocking); err == nil {
		t.Fatal("started after shutdown")
	}

	// A process that died mid-task leaves a running record and no lock.
	third, err := NewLocalTasks(dir)
	if err != nil {
		t.Fatal(err)
	}
	crashed := stopped
	crashed.TaskRef.TaskID = "c0ffee00c0ffee00c0ffee00c0ffee00"
	crashed.SubmissionKeyDigest = "other"
	crashed.State = "running"
	crashed.Error = nil
	if err := protectionAtomic(third.path(crashed.TaskRef.TaskID), crashed); err != nil {
		t.Fatal(err)
	}
	view, err := third.Get(crashed.TaskRef)
	if err != nil || view.State != "reconciling" || view.Error == nil || view.Error.Code != "interrupted" {
		t.Fatalf("interrupted %+v %v", view, err)
	}
}

// RepoSync wrapped as the first local task, over fake-ssh.
func TestRepoSyncLocalTask(t *testing.T) {
	s, _, soul, ss := sealedWorld(t)
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "usb")
	if _, err := s.RepoAdd("usb", repo); err != nil {
		t.Fatal(err)
	}
	if err := s.ServerNameSet(ctx, "box", "box"); err != nil {
		t.Fatal(err)
	}
	tasks, err := NewLocalTasks(s.Dirs.Data)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tasks.Shutdown(context.Background()) }()
	if _, err := s.StartRepoSyncTask(tasks, "box", "missing", "k0"); err == nil {
		t.Fatal("unknown repository accepted")
	}
	task, err := s.StartRepoSyncTask(tasks, "box", "usb", "k1")
	if err != nil {
		t.Fatal(err)
	}
	ch, unsubscribe, err := tasks.Watch(task.TaskRef)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	done := waitLocal(t, ch, func(v LocalTask) bool { return localTerminal(v.State) })
	if done.State != "succeeded" {
		t.Fatalf("sync %+v", done)
	}
	var sum RepoSyncSummary
	if err := json.Unmarshal(done.Result, &sum); err != nil || sum.Downloaded == 0 || sum.Snapshots != 2 || sum.Problems != 0 {
		t.Fatalf("summary %+v %v", sum, err)
	}
	if _, err := os.Stat(filepath.Join(repo, "box", vault.TreeFile)); err != nil {
		t.Fatal(err)
	}
	st := s.RepoStatuses("box", "box")
	if len(st) != 1 || !st[0].Has(soul) || !st[0].Has(ss) {
		t.Fatalf("repo state %+v", st)
	}
	list, err := tasks.List()
	if err != nil || len(list) != 1 || list[0].Kind != LocalTaskRepoSync || list[0].Subject.Repo != "usb" {
		t.Fatalf("list %+v %v", list, err)
	}
}
