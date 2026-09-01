package core

import (
	"fmt"
	"strings"
	"time"

	"worktracker/internal/appstate"
	"worktracker/internal/classifier"
	"worktracker/internal/model"
	"worktracker/internal/reporting"
)

type Engine struct {
	Config Config
	Store  *Store
}

func (e Engine) Today(now time.Time) (model.DayReport, error) {
	start, end := e.Config.ReportingPeriod(now)
	return e.day(start, end, true)
}

func (e Engine) Week(now time.Time) (model.WeekReport, error) {
	dayStart, _ := e.Config.ReportingPeriod(now)
	weekday := (int(dayStart.Weekday()) + 6) % 7
	monday := dayStart.AddDate(0, 0, -weekday)
	days := make([]model.DayReport, 0, weekday+1)
	for start := monday; !start.After(dayStart); start = start.AddDate(0, 0, 1) {
		end := start.AddDate(0, 0, 1)
		live := start.Equal(dayStart)
		if live {
			end = now.In(e.Config.Location())
		}
		day, err := e.day(start, end, live)
		if err != nil {
			return model.WeekReport{}, err
		}
		days = append(days, day)
	}
	weekly, err := e.Config.WeeklyEvaluator()
	if err != nil {
		return model.WeekReport{}, err
	}
	reporting.ApplyWeekToDateEvaluation(days, weekly)
	return reporting.SummarizeWeek(days, monday, now.In(e.Config.Location()), e.Config.WorkdaysPerWeek, weekly), nil
}

func (e Engine) Status(now time.Time) (model.StatusReport, error) {
	day, err := e.Today(now)
	if err != nil {
		return model.StatusReport{}, err
	}
	return reporting.StatusFromDayReport(day, now), nil
}

func (e Engine) day(start, end time.Time, live bool) (model.DayReport, error) {
	if e.Store == nil {
		return model.DayReport{}, fmt.Errorf("core store is required")
	}
	state := e.Store.Snapshot()
	windows := make([]model.WindowEvent, 0, len(state.Windows))
	for _, item := range state.Windows {
		windows = append(windows, model.WindowEvent{Start: item.Start, End: item.End, App: item.Executable, Title: item.Title, Locked: item.Locked})
	}
	afks := make([]model.AFKEvent, 0, len(state.AFK))
	for _, item := range state.AFK {
		afks = append(afks, model.AFKEvent{Start: item.Start, End: item.End, Status: item.Status})
	}
	contexts := make([]model.ContextEvent, 0, len(state.StoredContext)+len(state.HostContext))
	for _, item := range state.StoredContext {
		contexts = append(contexts, classifier.ParseContext(item.Start, item.End, item.Data))
	}
	for _, item := range state.HostContext {
		evidence := make(map[string]model.PassiveEvidence, len(item.Apps))
		for id, raw := range item.Apps {
			observation := appstate.Observation{Detector: id, State: strings.ToLower(raw.State), Available: raw.Available, ObservedAt: raw.ObservedAt}
			evidence[id] = appstate.DeriveEvidence(observation, appstate.PassiveWhen("playing"))
		}
		contexts = append(contexts, model.ContextEvent{Start: item.Start, End: item.End, SchemaVersion: 1, Location: item.Location, LocationEvidence: item.LocationEvidence, Health: item.Health, PassiveEvidence: evidence})
	}
	passive := make([]model.PassiveEvidenceEvent, 0, len(state.Browser))
	for _, observation := range state.Browser {
		passive = append(passive, observation.PassiveEvent(observation.ObservedAt, observation.ObservedAt))
	}
	segments := classifier.BuildWithPassive(windows, afks, contexts, passive, start, end, classifier.Options{
		AFKGrace: e.Config.AFKGrace, AutoEnd: e.Config.AutoEnd, EvidenceFreshness: e.Config.EvidenceFreshness,
		LockApps: e.Config.LockApps, LockTitleContains: e.Config.LockTitleContains,
	})
	daily, err := e.Config.DailyEvaluator()
	if err != nil {
		return model.DayReport{}, err
	}
	return reporting.BuildDay(start, end, segments, live, e.Config.AutoEnd, daily), nil
}
