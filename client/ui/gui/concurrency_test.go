package gui

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"valise/client/config"
	"valise/core/protection"
	"valise/ui/service"
)

// Work off the main thread reads words with T while the main thread
// changes the language; `go test -race` proves the swap is safe, and every
// read is one catalog's text, never a mix.
func TestTranslateWhileLanguageChanges(t *testing.T) {
	a, _ := world(t)
	c, err := parseCatalog([]byte(`{"format":"valise-locale/1","lang":"en","name":"English","messages":{"nav.servers":"Servers"}}`), baseCatalog)
	if err != nil {
		t.Fatal(err)
	}
	a.locales = map[string]localeEntry{"en": {id: "en", cat: c}}
	defer words.Store(baseCatalog)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if got := T("nav.servers"); got != "服务器" && got != "Servers" {
					t.Errorf("torn read: %q", got)
					return
				}
				_ = T("servers.agents", "n", 2)
			}
		}()
	}
	for i := 0; i < 2000; i++ {
		a.prefs.Lang = []string{"en", "", "gone"}[i%3]
		a.chooseLocale()
	}
	a.prefs.Lang = "gone"
	a.chooseLocale()
	close(stop)
	wg.Wait()
	if a.langErr == nil || words.Load() != baseCatalog {
		t.Fatalf("a missing language must fall back and say so: %v", a.langErr)
	}
}

// A plan confirmed in one window is confirmed there only, once; two
// windows in one process never see each other's confirmations.
func TestConfirmationsBelongToOneApp(t *testing.T) {
	newApp := func() *App {
		return New(&service.Service{Dirs: config.Dirs{Data: t.TempDir(), Cache: t.TempDir()}})
	}
	a, b := newApp(), newApp()
	ctx := context.Background()
	backup := func(x *App, id string) bool {
		ok, err := x.svc.BackupReview(ctx, "home", service.BackupPlan{PlanID: id})
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	protect := func(x *App, id string) bool {
		ok, err := x.svc.ProtectionReview(ctx, "home", protection.FrozenChange{PlanID: id})
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	a.confirm("backup-1")
	a.confirm("plan-1")
	if backup(b, "backup-1") || protect(b, "plan-1") {
		t.Fatal("another window took this window's confirmation")
	}
	// One click still confirms: the same window's review passes, once.
	if !backup(a, "backup-1") || backup(a, "backup-1") {
		t.Fatal("a confirmation must pass exactly once in its own window")
	}
	if !protect(a, "plan-1") || protect(a, "plan-1") {
		t.Fatal("a plan change must pass exactly once in its own window")
	}
	if backup(a, "never-confirmed") {
		t.Fatal("an unconfirmed plan passed")
	}
	// The review hooks run off the main thread while the window confirms.
	var wg sync.WaitGroup
	passed := make([]int, 2)
	for n, x := range []*App{a, b} {
		for i := 0; i < 50; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				id := fmt.Sprint("p", i)
				x.confirm(id)
				if n == 0 && !backup(x, id) {
					t.Errorf("window %d lost %s", n, id)
				}
			}()
		}
		passed[n] = 50
	}
	wg.Wait()
	for i := 0; i < passed[1]; i++ {
		if !backup(b, fmt.Sprint("p", i)) {
			t.Fatalf("window b lost p%d", i)
		}
	}
}
