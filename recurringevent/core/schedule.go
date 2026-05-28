package core

import "time"

type Scheduler interface {
	AddEvent(event *Event)
	GetNextOccurence(event *Event) *Event
}

type Schedule struct {
	events []*Event
}

func NewSchedule() *Schedule {
	return &Schedule{
		events: make([]*Event, 0),
	}
}

func (s *Schedule) AddEvent(event *Event) {
	s.events = append(s.events, event)
}

func (s *Schedule) GetNextOccurence(event *Event) *Event {
	return nil
}

func (s *Schedule) GetAllEventsBetween(startTime time.Time, endTime time.Time) []*Event {

	return nil
}

func (s *Schedule) getRecurrenceBetween(event *Event, startDate time.Time, endDate time.Time) []*Event {
	if startDate.Equal(event.startDate) {

	}
	return nil
}
