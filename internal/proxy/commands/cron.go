package commands

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

func init() {
	RegisterHelp(HelpTopic{
		Name: "interval",
		Text: []string{
			"Time interval format (used by %after, %every, %cron, and %on once)",
			"",
			"Intervals may be written in any of these forms:",
			"  N    N seconds",
			"  Ns   N seconds",
			"  Nm   N minutes",
			"  Nh   N hours",
			"  Nd   N days",
		},
	})

	cronHelp := []string{
		"Run a command at a designated time",
		"",
		"Usage:",
		"  %after <interval> <command>",
		"  %every <interval> <command>",
		"  %cron after|every <interval> <command>",
		"  %cron cancel|delete <id> ...",
		"  %cron",
		"",
		"%after runs <command> once, after <interval> has elapsed. %every runs it",
		"repeatedly, once per <interval>. %cron does the same but requires an",
		"explicit after/every keyword.",
		"",
		"Running %cron (or %after/%every) with no arguments lists all scheduled",
		"tasks. A task may be cancelled with \"%cron cancel <id>\" (delete is a",
		"synonym). <command> may contain \\n to run several commands in sequence.",
		"",
		"(see also: interval)",
	}
	RegisterHelp(HelpTopic{Name: "cron", Text: cronHelp})
	RegisterHelp(HelpTopic{Name: "after", Text: cronHelp})
	RegisterHelp(HelpTopic{Name: "every", Text: cronHelp})
}

// intervalRe matches an interval spec: digits with an optional s/m/h/d suffix.
var intervalRe = regexp.MustCompile(`^(\d+)([smhd]?)$`)

// parseInterval parses a tlily-style time interval (e.g. "30", "30s", "5m",
// "2h", "1d") into a Duration. It reports false if the text is not a valid
// interval. Ports TLily::Utils::parse_interval.
func parseInterval(s string) (time.Duration, bool) {
	m := intervalRe.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	unit := time.Second
	switch m[2] {
	case "m":
		unit = time.Minute
	case "h":
		unit = time.Hour
	case "d":
		unit = 24 * time.Hour
	}
	return time.Duration(n) * unit, true
}

// cronJob is a single scheduled task.
type cronJob struct {
	id           int
	command      string
	intervalText string // the raw interval as typed, for display
	interval     time.Duration
	repeat       bool
	nextRun      time.Time
	timer        *time.Timer
}

// CronTable holds a session's scheduled (%after / %every / %cron) tasks. It is
// safe for concurrent use: the command handler runs on the dispatch goroutine
// while timers fire on their own goroutines.
type CronTable struct {
	mu   sync.Mutex
	next int
	jobs map[int]*cronJob

	// fire re-injects a command line as if the user had typed it.
	fire func(line string)
	// announce publishes informational output to all clients.
	announce func(lines []string)
}

// NewCronTable returns an empty CronTable. fire re-dispatches a command line;
// announce publishes informational output.
func NewCronTable(fire func(line string), announce func(lines []string)) *CronTable {
	return &CronTable{
		jobs:     make(map[int]*cronJob),
		fire:     fire,
		announce: announce,
	}
}

const cronUsage = "(usage: %cron after|every <interval> <command>; see %help cron)"

// HandleCommand implements %after, %every, and %cron. kind is the command name
// without its leading % ("after", "every", or "cron"); args are the tokens that
// followed it; respond receives the command's immediate output.
func (c *CronTable) HandleCommand(kind string, args []string, respond func(lines []string)) {
	// No arguments: list all scheduled tasks (for any of the three commands).
	if len(args) == 0 {
		respond(c.list())
		return
	}

	// cancel / delete <id> ... (accepted for any of the three commands).
	if args[0] == "cancel" || args[0] == "delete" {
		respond(c.cancel(args[1:]))
		return
	}

	repeat := kind == "every"

	// An explicit after/every keyword overrides the default and is required for
	// %cron (which has no repeat default of its own).
	hadKeyword := false
	if args[0] == "after" || args[0] == "every" {
		repeat = args[0] == "every"
		args = args[1:]
		hadKeyword = true
	}
	if kind == "cron" && !hadKeyword {
		respond([]string{cronUsage})
		return
	}

	if len(args) < 2 {
		respond([]string{cronUsage})
		return
	}

	itext := args[0]
	cmd := strings.Join(args[1:], " ")
	interval, ok := parseInterval(itext)
	if !ok {
		respond([]string{cronUsage})
		return
	}

	id := c.schedule(cmd, itext, interval, repeat)

	word := "after"
	if repeat {
		word = "every"
	}
	respond([]string{fmt.Sprintf("(%s %s, I will run %q [id %d].)", word, itext, cmd, id)})
}

// schedule registers a new job and starts its timer, returning the new id.
func (c *CronTable) schedule(cmd, itext string, interval time.Duration, repeat bool) int {
	c.mu.Lock()
	defer c.mu.Unlock()

	id := c.next
	c.next++
	job := &cronJob{
		id:           id,
		command:      cmd,
		intervalText: itext,
		interval:     interval,
		repeat:       repeat,
		nextRun:      time.Now().Add(interval),
	}
	job.timer = time.AfterFunc(interval, func() { c.run(id) })
	c.jobs[id] = job
	return id
}

// run fires the job with the given id: it announces the firing, reschedules
// (repeat) or removes (one-shot) the job, then re-dispatches each \n-separated
// subcommand. The job is rescheduled/removed under the lock, but fire/announce
// run unlocked so a slow Lily send can't block the table.
func (c *CronTable) run(id int) {
	c.mu.Lock()
	job, ok := c.jobs[id]
	if !ok {
		c.mu.Unlock()
		return
	}
	cmd := job.command
	itext := job.intervalText
	if job.repeat {
		job.nextRun = time.Now().Add(job.interval)
		job.timer = time.AfterFunc(job.interval, func() { c.run(id) })
	} else {
		delete(c.jobs, id)
	}
	c.mu.Unlock()

	c.announce([]string{fmt.Sprintf("(%s has passed, running %q.)", itext, cmd)})
	for _, sub := range strings.Split(cmd, "\n") {
		if sub = strings.TrimSpace(sub); sub != "" {
			c.fire(sub)
		}
	}
}

// cancel stops and removes the jobs named by ids, returning a line per id.
func (c *CronTable) cancel(ids []string) []string {
	if len(ids) == 0 {
		return []string{"(usage: %cron cancel <id> ...)"}
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	var out []string
	for _, s := range ids {
		id, err := strconv.Atoi(s)
		if err != nil {
			out = append(out, fmt.Sprintf("(not a task id: %s)", s))
			continue
		}
		job, ok := c.jobs[id]
		if !ok {
			out = append(out, fmt.Sprintf("(no scheduled task with id %d)", id))
			continue
		}
		job.timer.Stop()
		delete(c.jobs, id)
		out = append(out, fmt.Sprintf("(cancelling task %d (%s))", id, job.command))
	}
	return out
}

// list returns a human-readable table of scheduled tasks.
func (c *CronTable) list() []string {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.jobs) == 0 {
		return []string{"(there are no scheduled tasks)"}
	}

	ids := make([]int, 0, len(c.jobs))
	for id := range c.jobs {
		ids = append(ids, id)
	}
	sort.Ints(ids)

	out := []string{fmt.Sprintf("%3s  %-19s  %-7s  %s", "Id", "Next run", "Repeat", "Command")}
	for _, id := range ids {
		job := c.jobs[id]
		repeat := "once"
		if job.repeat {
			repeat = "every " + job.intervalText
		}
		out = append(out, fmt.Sprintf("%3d  %-19s  %-7s  %s",
			id, job.nextRun.Format("2006-01-02 15:04:05"), repeat, job.command))
	}
	return out
}

// StopAll stops every timer and clears the table. Called on session teardown.
func (c *CronTable) StopAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, job := range c.jobs {
		job.timer.Stop()
		delete(c.jobs, id)
	}
}
