package core

import "time"

type Frequency string

const (
	RecurrenceTypeDaily   Frequency = "Daily"
	RecurrenceTypeWeekly  Frequency = "Weekly"
	RecurrenceTypeMonthly Frequency = "Monthly"
	RecurrenceTypeAnnual  Frequency = "Annual"
)

/*
- Repeats every MONDAY until Jan 2025 or until 10 occurence.
- Repeats every two weeks on Monday and Tuesday for 12PM-1PM
- Repeats every two months on Tuesday
- Repeats every two months on 2nd day of Month.
*/

type RecurrenceRule struct {
	RecurrenceType Frequency
	Interval       int
	ByWeekDay      []time.Weekday
	ByMonthDay     []int
}
