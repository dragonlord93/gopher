package scheduler

import (
	"sync"
	"time"

	"scheduler/internal/config"
)

// wheelEntry holds a scheduled job waiting to fire.
type wheelEntry struct {
	job  *config.Job
	laps int // how many full wheel revolutions until this fires
}

// TimingWheel is a circular buffer of slots.
// The hand advances one slot per tick (1 second).
// When the hand lands on a slot it fires all entries with laps==0,
// and decrements laps on the rest (they wait for the next revolution).
//
// This is O(1) insert and O(k) fire where k = jobs firing this tick.
// Zero DB reads during normal operation — the DB is only touched during refill.
type TimingWheel struct {
	mu      sync.Mutex
	slots   [][]wheelEntry // circular buffer, len = WheelSlots
	current int            // index of the slot the hand is currently on
	tickN   int64          // total ticks elapsed — used for laps calculation
}

func NewTimingWheel() *TimingWheel {
	slots := make([][]wheelEntry, config.WheelSlots)
	for i := range slots {
		slots[i] = make([]wheelEntry, 0)
	}
	return &TimingWheel{slots: slots}
}

// Schedule inserts a job into the correct slot based on its next_fire_at.
// If the job fires more than WheelSlots ticks away it gets a laps count > 0.
func (w *TimingWheel) Schedule(job *config.Job) {
	w.mu.Lock()
	defer w.mu.Unlock()

	delay := time.Until(job.NextFireAt)
	if delay < 0 {
		delay = 0 // already due — put in the very next slot
	}

	ticks := int(delay / config.WheelTickInterval)
	laps := ticks / config.WheelSlots
	slot := (w.current + ticks) % config.WheelSlots

	w.slots[slot] = append(w.slots[slot], wheelEntry{job: job, laps: laps})
}

// Tick advances the hand by one slot and returns all jobs that should fire now.
// Called by the scheduler loop every WheelTickInterval.
func (w *TimingWheel) Tick() []*config.Job {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.current = (w.current + 1) % config.WheelSlots
	w.tickN++

	entries := w.slots[w.current]
	w.slots[w.current] = w.slots[w.current][:0] // clear the slot

	var firing []*config.Job
	var remaining []wheelEntry

	for _, e := range entries {
		if e.laps == 0 {
			// This job fires now
			firing = append(firing, e.job)
		} else {
			// Still has revolutions to go — decrement and put back
			remaining = append(remaining, wheelEntry{job: e.job, laps: e.laps - 1})
		}
	}

	w.slots[w.current] = append(w.slots[w.current], remaining...)
	return firing
}

// RemoveShardJobs removes all pending wheel entries for a given shard.
// Called when this node loses ownership of a shard during rebalancing.
func (w *TimingWheel) RemoveShardJobs(shardID int) int {
	w.mu.Lock()
	defer w.mu.Unlock()

	removed := 0
	for i, slot := range w.slots {
		filtered := slot[:0]
		for _, e := range slot {
			if e.job.ShardID != shardID {
				filtered = append(filtered, e)
			} else {
				removed++
			}
		}
		w.slots[i] = filtered
	}
	return removed
}

// Size returns the total number of jobs currently in the wheel.
func (w *TimingWheel) Size() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	total := 0
	for _, slot := range w.slots {
		total += len(slot)
	}
	return total
}
