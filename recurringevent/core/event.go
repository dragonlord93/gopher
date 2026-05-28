package core

import "time"

type Event struct {
	id             string
	name           string
	description    string
	startDate      time.Time
	endDate        time.Time
	duration       time.Duration
	isRecurring    bool
	recurrenceRule []RecurrenceRule
}
