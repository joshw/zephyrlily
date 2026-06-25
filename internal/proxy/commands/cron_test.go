package commands

import (
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseInterval(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		{"30", 30 * time.Second, true},
		{"30s", 30 * time.Second, true},
		{"5m", 5 * time.Minute, true},
		{"2h", 2 * time.Hour, true},
		{"1d", 24 * time.Hour, true},
		{"0", 0, true},
		{"", 0, false},
		{"abc", 0, false},
		{"5x", 0, false},
		{"5 m", 0, false},
		{"-5", 0, false},
		{"m", 0, false},
	}
	for _, c := range cases {
		got, ok := parseInterval(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("parseInterval(%q) = (%v, %v), want (%v, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

// recorder captures fire/announce callbacks for assertions.
type recorder struct {
	mu        sync.Mutex
	fired     []string
	announced []string
}

func (r *recorder) fire(line string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fired = append(r.fired, line)
}

func (r *recorder) announce(lines []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.announced = append(r.announced, lines...)
}

func (r *recorder) firedCopy() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.fired...)
}

// collect returns a respond func that appends output to out.
func collect(out *[]string) func([]string) {
	return func(lines []string) { *out = append(*out, lines...) }
}

func TestCronAfterFiresOnce(t *testing.T) {
	r := &recorder{}
	c := NewCronTable(r.fire, r.announce)

	var out []string
	c.HandleCommand("after", []string{"1", ";test"}, collect(&out))
	if len(out) != 1 || !strings.Contains(out[0], "id 0") {
		t.Fatalf("unexpected schedule output: %v", out)
	}

	// Rewrite the timer to fire immediately rather than waiting a real second.
	if !runJobNow(c, 0) {
		t.Fatal("job 0 not found")
	}

	fired := r.firedCopy()
	if len(fired) != 1 || fired[0] != ";test" {
		t.Fatalf("expected single fire of ;test, got %v", fired)
	}
	// One-shot job should be gone.
	var list []string
	c.HandleCommand("cron", nil, collect(&list))
	if len(list) != 1 || !strings.Contains(list[0], "no scheduled tasks") {
		t.Fatalf("expected empty task list, got %v", list)
	}
}

func TestCronEveryReschedules(t *testing.T) {
	r := &recorder{}
	c := NewCronTable(r.fire, r.announce)

	var out []string
	c.HandleCommand("every", []string{"1h", ";ping"}, collect(&out))

	runJobNow(c, 0)
	runJobNow(c, 0)

	if got := len(r.firedCopy()); got != 2 {
		t.Fatalf("expected 2 fires from repeating job, got %d", got)
	}
	// Repeating job should still be listed.
	var list []string
	c.HandleCommand("cron", nil, collect(&list))
	if len(list) < 2 {
		t.Fatalf("expected job still listed, got %v", list)
	}
}

func TestCronMultiSubcommand(t *testing.T) {
	// A command containing a real newline fires as two separate subcommands.
	r := &recorder{}
	c := NewCronTable(r.fire, r.announce)
	c.schedule("a;hi\nb;bye", "1", time.Hour, false)
	runJobNow(c, 0)
	fired := r.firedCopy()
	if len(fired) != 2 || fired[0] != "a;hi" || fired[1] != "b;bye" {
		t.Fatalf("expected two subcommands, got %v", fired)
	}
}

func TestCronCancel(t *testing.T) {
	r := &recorder{}
	c := NewCronTable(r.fire, r.announce)
	var out []string
	c.HandleCommand("every", []string{"1h", ";ping"}, collect(&out))

	var cancelOut []string
	c.HandleCommand("cron", []string{"cancel", "0"}, collect(&cancelOut))
	if len(cancelOut) != 1 || !strings.Contains(cancelOut[0], "cancelling task 0") {
		t.Fatalf("unexpected cancel output: %v", cancelOut)
	}

	var list []string
	c.HandleCommand("cron", nil, collect(&list))
	if len(list) != 1 || !strings.Contains(list[0], "no scheduled tasks") {
		t.Fatalf("expected empty list after cancel, got %v", list)
	}
}

func TestCronBareCronNeedsKeyword(t *testing.T) {
	r := &recorder{}
	c := NewCronTable(r.fire, r.announce)
	var out []string
	c.HandleCommand("cron", []string{"5s", ";nope"}, collect(&out))
	if len(out) != 1 || !strings.Contains(out[0], "usage") {
		t.Fatalf("expected usage error for keyword-less %%cron, got %v", out)
	}
	if len(r.firedCopy()) != 0 {
		t.Fatal("nothing should have been scheduled")
	}
}

func TestCronBadInterval(t *testing.T) {
	r := &recorder{}
	c := NewCronTable(r.fire, r.announce)
	var out []string
	c.HandleCommand("after", []string{"soon", ";nope"}, collect(&out))
	if len(out) != 1 || !strings.Contains(out[0], "usage") {
		t.Fatalf("expected usage error for bad interval, got %v", out)
	}
}

// runJobNow synchronously runs the job with the given id (bypassing its timer)
// and returns whether the job existed. It stops the pending timer first so the
// real one never fires during the test.
func runJobNow(c *CronTable, id int) bool {
	c.mu.Lock()
	job, ok := c.jobs[id]
	if ok && job.timer != nil {
		job.timer.Stop()
	}
	c.mu.Unlock()
	if !ok {
		return false
	}
	c.run(id)
	return true
}
